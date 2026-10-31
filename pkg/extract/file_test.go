package extract

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"

	packer "github.com/arenadata/oci-packer"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

// packed builds a pack of a file and a directory into a layout and tags it.
func packed(t *testing.T) (registry.Resolver, reference.Reference) {
	t.Helper()
	work := t.TempDir()
	t.Chdir(work)
	_ = os.WriteFile("schema.json", []byte(`{"type":"object"}`), 0o644)
	_ = os.MkdirAll("templates/nested", 0o755)
	_ = os.WriteFile("templates/app.tmpl", []byte("a={{ .a }}\n"), 0o644)
	_ = os.WriteFile("templates/nested/b.tmpl", []byte("b\n"), 0o644)

	res, err := layout.New("oci://" + filepath.Join(work, "layout") + ":packs/app:1.0")
	if err != nil {
		t.Fatal(err)
	}
	p := packer.Pack{
		Type: "application/vnd.example.pack.v1+json",
		Metadata: packer.Metadata{Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "app"}},
		Items: []packer.Descriptor{
			{From: "file://schema.json", Type: "application/vnd.example.schema.v1+json"},
			{From: "dir://templates/", Type: "application/vnd.example.template.v1"},
		},
	}
	desc, err := packer.Build(context.Background(), p, res)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.SetTag(context.Background(), desc); err != nil {
		t.Fatal(err)
	}
	return res, reference.Reference{Ref: "packs/app:1.0"}
}

// The files an artifact packs itself, at the paths of its tree.
func TestList(t *testing.T) {
	res, ref := packed(t)
	files, err := List(context.Background(), res, ref, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	want := []string{"schema.json", "templates/app.tmpl", "templates/nested/b.tmpl"}
	if !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

// One file is read, and only its layer.
func TestOpen(t *testing.T) {
	res, ref := packed(t)
	for path, want := range map[string]string{
		"schema.json":              `{"type":"object"}`,
		"templates/app.tmpl":       "a={{ .a }}\n",
		"/templates/nested/b.tmpl": "b\n", // a leading slash is the tree's root
	} {
		desc, r, err := Open(context.Background(), res, ref, path, Options{})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		if string(b) != want || desc.Size != int64(len(want)) {
			t.Errorf("%s: %q (%d bytes), want %q", path, b, desc.Size, want)
		}
	}
}

func TestOpen_NoSuchFile(t *testing.T) {
	res, ref := packed(t)
	_, _, err := Open(context.Background(), res, ref, "templates/missing.tmpl", Options{})
	if !errors.Is(err, ErrNoFile) {
		t.Fatalf("err = %v", err)
	}
	// the message says what the artifact does pack
	if want := "schema.json, templates/app.tmpl"; !strings.Contains(err.Error(), want) {
		t.Errorf("%q does not name %q", err, want)
	}
}

// Descriptors are the files as proxy.FetchDescriptors wants them: titled by the path in the tree, the layers'
// own annotations left alone.
func TestDescriptors(t *testing.T) {
	src, ref := packed(t)
	ds, err := Options{}.Descriptors(context.Background(), src, ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, d := range ds {
		titles = append(titles, d.Annotations[ocispecv1.AnnotationTitle])
	}
	files, _ := List(context.Background(), src, ref, Options{})
	if len(ds) != len(files) || len(ds) == 0 {
		t.Fatalf("%d descriptors for %d files", len(ds), len(files))
	}
	for i, f := range files {
		if titles[i] != f.Path || ds[i].Digest != f.Desc.Digest {
			t.Errorf("%d: %q (%s), want %q", i, titles[i], ds[i].Digest, f.Path)
		}
	}
}
