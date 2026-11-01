// Package extract is the pack file's inverse: from an artifact in an OCI layout
// it writes the directory that would rebuild it — pack.yaml, every packed file
// at its title (under the directory a dir:// item named), and, when asked, the
// config of a mounted image as image.json. A mounted member — an image, or
// another artifact — becomes a oci://<registry>/<repo>@<digest> item, and an
// artifact among them is extracted as well, into a sibling directory of its
// own, so that a pack of packs comes out as one flat tree of directories.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"

	packer "github.com/arenadata/oci-packer"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

type Options struct {
	// Repo is the repository the artifact lives in (packs/adh): mounted
	// members were copied into it, so that is where their digests resolve.
	Repo string
	// NameBy is the annotation that names the directory of an artifact
	// (default org.opencontainers.image.title); without it, the short digest.
	NameBy string
	// ImageConfig writes a mounted image's config as image.json.
	ImageConfig bool
}

func (o Options) withDefaults() Options {
	if o.NameBy == "" {
		o.NameBy = ocispecv1.AnnotationTitle
	}
	return o
}

// Extract writes the artifact ref names, and every artifact mounted into it,
// under dst — one directory per artifact — and returns the root's directory.
func Extract(ctx context.Context, src registry.Resolver, ref reference.Reference, dst string, o Options) (string, error) {
	o = o.withDefaults()
	desc, err := src.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	e := &extractor{src: src, dst: dst, repo: o.Repo, o: o, seen: map[digest.Digest]bool{}}
	fallback := o.Repo
	if i := strings.LastIndex(fallback, "/"); i >= 0 {
		fallback = fallback[i+1:]
	}
	return e.artifact(ctx, desc, fallback)
}

type extractor struct {
	src  registry.Fetcher
	dst  string
	repo string
	o    Options
	seen map[digest.Digest]bool
}

// artifact extracts one index or manifest into its own directory.
func (e *extractor) artifact(ctx context.Context, desc ocispecv1.Descriptor, fallback string) (string, error) {
	annotations, err := e.annotationsOf(ctx, desc)
	if err != nil {
		return "", err
	}
	name := annotations[e.o.NameBy]
	if name == "" {
		name = fallback
	}
	if name == "" {
		name = desc.Digest.Encoded()[:12]
	}

	dir := filepath.Join(e.dst, name)
	if e.seen[desc.Digest] {
		return dir, nil
	}

	e.seen[desc.Digest] = true
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	pf := packer.Pack{}
	st, err := e.read(ctx, desc)
	if err != nil {
		return "", err
	}

	if st.Index != nil {
		pf.Type = st.Index.ArtifactType
		pf.Annotations = strip(st.Index.Annotations)

		parts, err := e.parts(ctx, st.Index)
		if err != nil {
			return "", fmt.Errorf("%s: %w", desc.Digest, err)
		}

		for _, p := range parts {
			items, err := e.item(ctx, p, dir)
			if err != nil {
				return "", fmt.Errorf("%s: %w", desc.Digest, err)
			}
			pf.Items = append(pf.Items, items...)
		}
	} else {
		man := st.Manifest
		pf.Type = man.ArtifactType
		pf.Annotations = strip(man.Annotations)

		if !isEmptyConfig(man.Config) {
			if err := e.writeBlob(ctx, man.Config, filepath.Join(dir, "config.json")); err != nil {
				return "", err
			}
			pf.Config = &packer.ConfigDescriptor{From: "file://config.json", Type: man.Config.MediaType}
		}

		items, err := e.files(ctx, man.Layers, dir)
		if err != nil {
			return "", err
		}
		pf.Items = items
	}

	raw, err := yaml.Marshal(pf)
	if err != nil {
		return "", err
	}

	return dir, os.WriteFile(filepath.Join(dir, "pack.yaml"), raw, 0o644)
}

// structure is what an artifact's descriptor holds: an index, or a manifest.
type structure struct {
	Index    *ocispecv1.Index
	Manifest *ocispecv1.Manifest
}

// read fetches the index or the manifest desc names; any other media type is no artifact.
func (e *extractor) read(ctx context.Context, desc ocispecv1.Descriptor) (structure, error) {
	switch {
	case isIndex(desc.MediaType):
		var idx ocispecv1.Index
		if err := e.fetchJSON(ctx, desc, &idx); err != nil {
			return structure{}, err
		}
		return structure{Index: &idx}, nil
	case isManifest(desc.MediaType):
		var man ocispecv1.Manifest
		if err := e.fetchJSON(ctx, desc, &man); err != nil {
			return structure{}, err
		}
		return structure{Manifest: &man}, nil
	}
	return structure{}, fmt.Errorf("%s: media type %q is neither an index nor a manifest", desc.Digest, desc.MediaType)
}

// kind is what a member of an index is.
type kind int

const (
	kindFiles    kind = iota // a manifest built from files: the files of this artifact
	kindImage                // a mounted image
	kindArtifact             // a mounted index, or a manifest of files that names itself: an artifact of its own
)

// part is a member of an index: its entry, what it is, and — for a manifest — the manifest.
type part struct {
	Desc     ocispecv1.Descriptor
	Kind     kind
	Manifest ocispecv1.Manifest
}

// parts classifies the members of an index. This is the one place that says which of them are files of the
// artifact itself (extract writes them, List lists them) and which are mounted.
func (e *extractor) parts(ctx context.Context, idx *ocispecv1.Index) ([]part, error) {
	out := make([]part, 0, len(idx.Manifests))
	for _, m := range idx.Manifests {
		p := part{Desc: m, Kind: kindArtifact}
		if isIndex(m.MediaType) {
			image, err := e.imageIndex(ctx, m)
			if err != nil {
				return nil, err
			}
			if image {
				p.Kind = kindImage // a multi-platform image: mounted, not unpacked
			}
		} else {
			if err := e.fetchJSON(ctx, m, &p.Manifest); err != nil {
				return nil, err
			}
			switch {
			case !isEmptyConfig(p.Manifest.Config):
				p.Kind = kindImage
			case e.names(m, p.Manifest.Annotations):
				// A manifest of files that names itself (carries the annotation --name-by points at, on the manifest
				// or its entry) is an artifact of its own, mounted among the members — not files of this one.
			default:
				p.Kind = kindFiles
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// imageIndex reports whether an index is a multi-platform image rather than an artifact: it holds images (a
// manifest with a config of its own) and, besides them, only the attestations a builder adds (platform
// unknown/unknown) — no manifest of files. Its layers and attestations are the registry's, not files to write.
// An index that mixes an image with files built from a pack file is a component, and is unpacked.
func (e *extractor) imageIndex(ctx context.Context, m ocispecv1.Descriptor) (bool, error) {
	var idx ocispecv1.Index
	if err := e.fetchJSON(ctx, m, &idx); err != nil {
		return false, err
	}
	var images int
	for _, c := range idx.Manifests {
		if isIndex(c.MediaType) {
			return false, nil
		}
		if c.Platform != nil && c.Platform.OS == "unknown" && c.Platform.Architecture == "unknown" {
			continue // an attestation
		}
		var man ocispecv1.Manifest
		if err := e.fetchJSON(ctx, c, &man); err != nil {
			return false, err
		}
		if isEmptyConfig(man.Config) {
			return false, nil // files built from a pack file
		}
		images++
	}
	return images > 0, nil
}

// item turns one member of an index into pack-file items: a built manifest into its files, a mounted image into
// a oci:// item (plus image.json on request), a mounted index or named artifact into a oci:// item and an
// artifact of its own.
func (e *extractor) item(ctx context.Context, p part, dir string) ([]packer.Descriptor, error) {
	m := p.Desc
	mounted := packer.Descriptor{From: e.remote(m.Digest), Platform: formatPlatform(m.Platform), Annotations: strip(m.Annotations)}
	switch p.Kind {
	case kindArtifact:
		if _, err := e.artifact(ctx, m, m.Digest.Encoded()[:12]); err != nil {
			return nil, err
		}
		return []packer.Descriptor{mounted}, nil
	case kindImage:
		if e.o.ImageConfig && !isIndex(p.Desc.MediaType) {
			if err := e.writeBlob(ctx, p.Manifest.Config, filepath.Join(dir, "image.json")); err != nil {
				return nil, err
			}
		}
		return []packer.Descriptor{mounted}, nil
	}
	items, err := e.files(ctx, p.Manifest.Layers, dir)
	if err != nil {
		return nil, err
	}
	// a built member: its type and platform sit on the member, not its layers
	for i := range items {
		if items[i].Type == "" {
			items[i].Type = m.ArtifactType
		}
		if items[i].Platform == "" {
			items[i].Platform = formatPlatform(m.Platform)
		}
	}
	return items, nil
}

// names reports whether a member says what it is called: the annotation that names an artifact's directory is on
// its entry in the index or on the manifest itself.
func (e *extractor) names(m ocispecv1.Descriptor, manifest map[string]string) bool {
	return m.Annotations[e.o.NameBy] != "" || manifest[e.o.NameBy] != ""
}

// files writes a built manifest's layers at their titles and returns the items
// that packed them: one dir:// item when every layer came from one directory,
// file:// items otherwise.
func (e *extractor) files(ctx context.Context, layers []ocispecv1.Descriptor, dir string) ([]packer.Descriptor, error) {
	var items []packer.Descriptor
	dirs := map[string]bool{}
	for _, l := range layers {
		rel, err := layerPath(l)
		if err != nil {
			return nil, err
		}
		sub := l.Annotations[packer.AnnotationDir]
		title := l.Annotations[ocispecv1.AnnotationTitle]
		if title == "" {
			title = l.Digest.Encoded()[:12]
		}
		if err := e.writeBlob(ctx, l, filepath.Join(dir, rel)); err != nil {
			return nil, err
		}
		if sub != "" {
			if !dirs[sub] {
				dirs[sub] = true
				items = append(items, packer.Descriptor{From: "dir://" + sub + "/", Type: l.ArtifactType, Platform: formatPlatform(l.Platform)})
			}
			continue
		}
		ann := strip(l.Annotations)
		delete(ann, ocispecv1.AnnotationTitle)
		items = append(items, packer.Descriptor{From: "file://" + rel, Type: l.ArtifactType, Platform: formatPlatform(l.Platform), Annotations: ann})
	}
	return items, nil
}

// layerPath is where a packed file sits in the artifact's tree: its title, under the directory a dir:// item named.
func layerPath(l ocispecv1.Descriptor) (string, error) {
	title := l.Annotations[ocispecv1.AnnotationTitle]
	if title == "" {
		title = l.Digest.Encoded()[:12]
	}
	rel := title
	if sub := l.Annotations[packer.AnnotationDir]; sub != "" {
		rel = filepath.Join(sub, title)
	}
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("layer %s: title %q escapes the directory", l.Digest, rel)
	}
	return filepath.ToSlash(rel), nil
}

// remote is the item of a mounted member: its digest in the artifact's
// repository — an item names no registry, the pack's own is meant.
func (e *extractor) remote(d digest.Digest) string {
	return reference.OciScheme.String() + e.repo + "@" + d.String()
}

// annotationsOf reads the artifact's own annotations and lays the
// descriptor's over them: a layout's index entry carries only ref.name, the
// index blob carries what the author wrote.
func (e *extractor) annotationsOf(ctx context.Context, desc ocispecv1.Descriptor) (map[string]string, error) {
	var head struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := e.fetchJSON(ctx, desc, &head); err != nil {
		return nil, err
	}
	out := map[string]string{}
	maps.Copy(out, head.Annotations)
	maps.Copy(out, desc.Annotations)
	return out, nil
}

func (e *extractor) fetchJSON(ctx context.Context, desc ocispecv1.Descriptor, v any) error {
	r, err := e.src.Fetch(ctx, reference.Reference{Ref: desc.Digest.String()}.WithDescriptor(desc))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return json.NewDecoder(r).Decode(v)
}

func (e *extractor) writeBlob(ctx context.Context, desc ocispecv1.Descriptor, path string) error {
	r, err := e.src.Fetch(ctx, reference.Reference{Ref: desc.Digest.String()}.WithDescriptor(desc))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

// strip drops the annotations the packer adds by itself, so the written pack
// file carries only what the author wrote; nil when nothing is left.
func strip(a map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		switch k {
		case ocispecv1.AnnotationCreated, ocispecv1.AnnotationRefName, packer.AnnotationDir:
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func formatPlatform(p *ocispecv1.Platform) string {
	if p == nil {
		return ""
	}
	return platforms.FormatAll(*p)
}

func isIndex(mt string) bool {
	return mt == ocispecv1.MediaTypeImageIndex || mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

func isManifest(mt string) bool {
	return mt == ocispecv1.MediaTypeImageManifest || mt == "application/vnd.docker.distribution.manifest.v2+json"
}

func isEmptyConfig(d ocispecv1.Descriptor) bool {
	return d.Digest == ocispecv1.DescriptorEmptyJSON.Digest || d.MediaType == ocispecv1.MediaTypeEmptyJSON
}

// sortedKeys is here for deterministic pack files in tests.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
