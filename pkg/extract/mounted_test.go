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
