// Package extract is the pack file's inverse: from an artifact in an OCI layout
// it writes the directory that would rebuild it — pack.yaml, every packed file
// at its title (under the directory a dir:// item named), and, when asked, the
// config of a mounted image as image.json. A mounted member — an image, or
// another artifact — becomes a cr://<registry>/<repo>@<digest> item, and an
// artifact among them is extracted as well, into a sibling directory of its
// own, so that a pack of packs comes out as one flat tree of directories.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// Registry is the host the artifact lives in — what cr:// items of the
	// written pack file point at.
	Registry string
	// Repo is the repository the artifact lives in (packs/adh): mounted
	// members were copied into it, so that is where their digests resolve.
	Repo string
	// NameBy is the annotation that names the directory of an artifact
	// (default org.opencontainers.image.title); without it, the short digest.
	NameBy string
	// ImageConfig writes a mounted image's config as image.json.
	ImageConfig bool
}

// Extract writes the artifact ref names, and every artifact mounted into it,
// under dst — one directory per artifact — and returns the root's directory.
func Extract(ctx context.Context, src registry.Resolver, ref reference.Reference, dst string, o Options) (string, error) {
	if o.NameBy == "" {
		o.NameBy = ocispecv1.AnnotationTitle
	}
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
	switch {
	case isIndex(desc.MediaType):
		var idx ocispecv1.Index
		if err := e.fetchJSON(ctx, desc, &idx); err != nil {
			return "", err
		}
		pf.Type = idx.ArtifactType
		pf.Annotations = strip(idx.Annotations)
		for _, m := range idx.Manifests {
			items, err := e.member(ctx, m, dir)
			if err != nil {
				return "", fmt.Errorf("%s: %w", desc.Digest, err)
			}
			pf.Items = append(pf.Items, items...)
		}
	case isManifest(desc.MediaType):
		var man ocispecv1.Manifest
		if err := e.fetchJSON(ctx, desc, &man); err != nil {
			return "", err
		}
		pf.Type = man.ArtifactType
		pf.Annotations = strip(man.Annotations)
		if !isEmptyConfig(man.Config) {
			if err := e.writeBlob(ctx, man.Config, filepath.Join(dir, "config.json")); err != nil {
				return "", err
			}
			pf.Config = &packer.ConfigDescriptor{From: "file://config.json", Type: man.Config.MediaType}
		}
		items, err := e.files(ctx, man, dir)
		if err != nil {
			return "", err
		}
		pf.Items = items
	default:
		return "", fmt.Errorf("%s: media type %q is neither an index nor a manifest", desc.Digest, desc.MediaType)
	}
	raw, err := yaml.Marshal(pf)
	if err != nil {
		return "", err
	}
	return dir, os.WriteFile(filepath.Join(dir, "pack.yaml"), raw, 0o644)
}

// member turns one member of an index into pack-file items: a built manifest
// (empty config) into its files, a mounted image into a cr:// item (plus
// image.json on request), a mounted index into a cr:// item and an artifact
// of its own.
func (e *extractor) member(ctx context.Context, m ocispecv1.Descriptor, dir string) ([]packer.Descriptor, error) {
	mounted := packer.Descriptor{From: e.cr(m.Digest), Platform: formatPlatform(m.Platform), Annotations: strip(m.Annotations)}
	if isIndex(m.MediaType) {
		if _, err := e.artifact(ctx, m, m.Digest.Encoded()[:12]); err != nil {
			return nil, err
		}
		return []packer.Descriptor{mounted}, nil
	}
	var man ocispecv1.Manifest
	if err := e.fetchJSON(ctx, m, &man); err != nil {
		return nil, err
	}
	if !isEmptyConfig(man.Config) {
		if e.o.ImageConfig {
			if err := e.writeBlob(ctx, man.Config, filepath.Join(dir, "image.json")); err != nil {
				return nil, err
			}
		}
		return []packer.Descriptor{mounted}, nil
	}
	items, err := e.files(ctx, man, dir)
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

// files writes a built manifest's layers at their titles and returns the items
// that packed them: one dir:// item when every layer came from one directory,
// file:// items otherwise.
func (e *extractor) files(ctx context.Context, man ocispecv1.Manifest, dir string) ([]packer.Descriptor, error) {
	var items []packer.Descriptor
	dirs := map[string]bool{}
	for _, l := range man.Layers {
		title := l.Annotations[ocispecv1.AnnotationTitle]
		if title == "" {
			title = l.Digest.Encoded()[:12]
		}
		sub := l.Annotations[packer.AnnotationDir]
		rel := title
		if sub != "" {
			rel = filepath.Join(sub, title)
		}
		if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("layer %s: title %q escapes the directory", l.Digest, rel)
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

func (e *extractor) cr(d digest.Digest) string {
	host := e.o.Registry
	if host != "" && !strings.HasSuffix(host, "/") {
		host += "/"
	}
	return "cr://" + host + e.repo + "@" + d.String()
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
	for k, v := range head.Annotations {
		out[k] = v
	}
	for k, v := range desc.Annotations {
		out[k] = v
	}
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
