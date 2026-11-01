package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"

	packer "github.com/arenadata/oci-packer"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

// An artifact of files only is a manifest. Mounted among the members of another, it must come out as a directory
// of its own — not as files of the one that holds it, whose schema.json it would overwrite.
func TestExtract_MountedManifestArtifactIsADirectoryOfItsOwn(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	ctx := context.Background()
	lay := filepath.Join(work, "layout")

	build := func(name string, files map[string]string) (*layout.Layout, ocispecv1.Descriptor) {
		dir := filepath.Join(work, "src", name)
		for f, body := range files {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
			_ = os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
		}
		t.Chdir(dir)
		r, err := layout.New("oci://" + lay + ":packs/" + name + ":1")
		if err != nil {
			t.Fatal(err)
		}
		desc, err := packer.Build(ctx, packer.Pack{
			Type:     "application/vnd.example.pack.v1+json",
			Metadata: packer.Metadata{Annotations: map[string]string{"io.example.name": name}},
			Items:    []packer.Descriptor{{From: "file://schema.json", Type: "application/vnd.example.schema.v1+json"}},
		}, r)
		if err != nil {
			t.Fatal(err)
		}
		return r.(*layout.Layout), desc
	}
	_, inner := build("inner", map[string]string{"schema.json": `{"inner":true}`})
	res, outerOwn := build("outer", map[string]string{"schema.json": `{"outer":true}`})

	// the holder: an index of its own files, and the artifact it holds
	outerOwn.ArtifactType = "application/vnd.example.schema.v1+json"
	idx := ocispecv1.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageIndex,
		ArtifactType: "application/vnd.example.pack.v1+json", Manifests: []ocispecv1.Descriptor{outerOwn, inner},
		Annotations: map[string]string{"io.example.name": "outer"}}
	body, _ := json.Marshal(idx)
	top := ocispecv1.Descriptor{MediaType: ocispecv1.MediaTypeImageIndex, Digest: digest.FromBytes(body), Size: int64(len(body)),
		Annotations: map[string]string{"io.example.name": "outer", ocispecv1.AnnotationRefName: "packs/outer:1"}}
	if err := res.Push(ctx, top, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := res.SetTag(ctx, top); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(work, "out")
	dir, err := Extract(ctx, res, reference.Reference{Ref: "packs/outer:1"}, out, Options{Repo: "packs/outer", NameBy: "io.example.name"})
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "outer") {
		t.Fatalf("dir = %s", dir)
	}
	for path, want := range map[string]string{"outer/schema.json": `{"outer":true}`, "inner/schema.json": `{"inner":true}`} {
		got, err := os.ReadFile(filepath.Join(out, path))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q, %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "inner", "pack.yaml")); err != nil {
		t.Errorf("the held artifact has a pack file of its own: %v", err)
	}
}

// A multi-platform image mounted in a pack — images and the attestations a builder adds — is a oci:// item and
// nothing more: its attestations are not written into a directory of their own.
func TestExtract_MountedImageIndexIsNotUnpacked(t *testing.T) {
	work := t.TempDir()
	res, err := layout.New("oci://" + filepath.Join(work, "layout") + ":packs/app:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	push := func(mt string, body []byte, extra map[string]string) ocispecv1.Descriptor {
		d := ocispecv1.Descriptor{MediaType: mt, Digest: digest.FromBytes(body), Size: int64(len(body)), Annotations: extra}
		if err := res.Push(ctx, d, bytes.NewReader(body)); err != nil && !registry.IsAlreadyExists(err) {
			t.Fatal(err)
		}
		return d
	}
	manifest := func(cfg ocispecv1.Descriptor, layers ...ocispecv1.Descriptor) ocispecv1.Descriptor {
		body, _ := json.Marshal(ocispecv1.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageManifest, Config: cfg, Layers: layers})
		return push(ocispecv1.MediaTypeImageManifest, body, nil)
	}
	cfg := push(ocispecv1.MediaTypeImageConfig, []byte(`{"architecture":"amd64","os":"linux"}`), nil)
	img := manifest(cfg, push(ocispecv1.MediaTypeImageLayerGzip, []byte("layer"), nil))
	img.Platform = &ocispecv1.Platform{OS: "linux", Architecture: "amd64"}
	empty := push(ocispecv1.MediaTypeEmptyJSON, ocispecv1.DescriptorEmptyJSON.Data, nil)
	att := manifest(empty, push("application/vnd.in-toto+json", []byte(`{"predicate":1}`), map[string]string{ocispecv1.AnnotationTitle: "att.json"}))
	att.Platform = &ocispecv1.Platform{OS: "unknown", Architecture: "unknown"}
	imgIdxBody, _ := json.Marshal(ocispecv1.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{img, att}})
	imgIdx := push(ocispecv1.MediaTypeImageIndex, imgIdxBody, nil)

	// the pack: its own file, and the image mounted
	own := manifest(empty, push("application/json", []byte(`{}`), map[string]string{ocispecv1.AnnotationTitle: "schema.json"}))
	packBody, _ := json.Marshal(ocispecv1.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageIndex,
		ArtifactType: "application/vnd.example.pack.v1+json", Manifests: []ocispecv1.Descriptor{own, imgIdx},
		Annotations: map[string]string{"io.example.name": "app"}})
	top := push(ocispecv1.MediaTypeImageIndex, packBody, map[string]string{"io.example.name": "app"})
	if err := res.SetTag(ctx, top); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(work, "out")
	if _, err := Extract(ctx, res, reference.Reference{Ref: "packs/app:1"}, out, Options{Repo: "packs/app", NameBy: "io.example.name"}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(out)
	if len(ents) != 1 || ents[0].Name() != "app" {
		t.Fatalf("directories = %v: the image index is no artifact of its own", ents)
	}
	files, err := List(ctx, res, reference.Reference{Ref: "packs/app:1"}, Options{NameBy: "io.example.name"})
	if err != nil || len(files) != 1 || files[0].Path != "schema.json" {
		t.Fatalf("files = %+v, %v", files, err)
	}
}
