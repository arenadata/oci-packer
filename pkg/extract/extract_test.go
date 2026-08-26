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
	"gopkg.in/yaml.v3"

	packer "github.com/arenadata/oci-packer"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

// A pack of a file and a directory, packed and extracted, comes back as the
// same tree with a pack.yaml that names the same items.
func TestExtract_RoundTripsFilesAndDirectories(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	os.WriteFile("schema.json", []byte(`{"type":"object"}`), 0o644)
	os.MkdirAll("templates/nested", 0o755)
	os.WriteFile("templates/app.tmpl", []byte("a={{ .a }}\n"), 0o644)
	os.WriteFile("templates/nested/b.tmpl", []byte("b\n"), 0o644)

	layoutDir := filepath.Join(work, "layout")
	res, err := layout.New("oci://" + layoutDir + ":components/alpine:0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	p := packer.Pack{
		Type: "application/vnd.example.component.v1+json",
		Metadata: packer.Metadata{Annotations: map[string]string{
			ocispecv1.AnnotationTitle: "alpine", "io.example.name": "alpine", "io.example.version": "0.1.0"}},
		Items: []packer.Descriptor{
			{From: "file://schema.json", Type: "application/vnd.example.schema.v1+json", Platform: "linux/amd64"},
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

	out := filepath.Join(work, "out")
	dir, err := Extract(context.Background(), res, reference.Reference{Ref: "components/alpine:0.1.0"}, out, Options{Repo: "components/alpine"})
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "alpine") {
		t.Fatalf("named by the title annotation: %s", dir)
	}
	for path, want := range map[string]string{
		"schema.json":             `{"type":"object"}`,
		"templates/app.tmpl":      "a={{ .a }}\n",
		"templates/nested/b.tmpl": "b\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", path, got, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	var back packer.Pack
	if err := yaml.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Type != p.Type || back.Annotations["io.example.version"] != "0.1.0" || back.Annotations[ocispecv1.AnnotationCreated] != "" {
		t.Errorf("pack.yaml head: %+v", back)
	}
	if len(back.Items) != 2 || back.Items[0].From != "file://schema.json" || back.Items[0].Platform != "linux/amd64" ||
		back.Items[1].From != "dir://templates/" || back.Items[1].Type != "application/vnd.example.template.v1" {
		t.Errorf("pack.yaml items: %+v", back.Items)
	}
}

// An index with a mounted image and a mounted artifact: the image becomes a
// cr:// item (and image.json on request), the artifact a cr:// item and a
// sibling directory of its own, named by --name-by.
func TestExtract_MountedMembersBecomeReferences(t *testing.T) {
	work := t.TempDir()
	res, err := layout.New("oci://" + filepath.Join(work, "layout") + ":packs/demo:1.0")
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
	// a mounted image: config + layer + manifest
	cfg := push(ocispecv1.MediaTypeImageConfig, []byte(`{"architecture":"arm64","os":"linux","config":{"Cmd":["/bin/sh"]}}`), nil)
	layer := push(ocispecv1.MediaTypeImageLayerGzip, []byte("layer-bytes"), nil)
	imgBody, _ := json.Marshal(ocispecv1.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageManifest, Config: cfg, Layers: []ocispecv1.Descriptor{layer}})
	img := push(ocispecv1.MediaTypeImageManifest, imgBody, nil)
	img.Platform = &ocispecv1.Platform{OS: "linux", Architecture: "arm64"}
	// a built member: empty config, one titled layer
	schema := push("application/json", []byte(`{"type":"object"}`), map[string]string{ocispecv1.AnnotationTitle: "schema.json"})
	empty := push(ocispecv1.MediaTypeEmptyJSON, ocispecv1.DescriptorEmptyJSON.Data, nil)
	builtBody, _ := json.Marshal(ocispecv1.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageManifest, ArtifactType: "application/vnd.example.schema.v1+json", Config: empty, Layers: []ocispecv1.Descriptor{schema}})
	built := push(ocispecv1.MediaTypeImageManifest, builtBody, nil)
	built.ArtifactType = "application/vnd.example.schema.v1+json"
	// the component: an index of the image and the built member
	compBody, _ := json.Marshal(ocispecv1.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageIndex, ArtifactType: "application/vnd.example.component.v1+json",
		Manifests: []ocispecv1.Descriptor{img, built}, Annotations: map[string]string{"io.example.name": "alpine"}})
	comp := push(ocispecv1.MediaTypeImageIndex, compBody, map[string]string{"io.example.name": "alpine"})
	comp.ArtifactType = "application/vnd.example.component.v1+json"
	// the pack: an index of the composition (built) and the component (mounted)
	composition := push("application/json", []byte(`{"properties":{}}`), map[string]string{ocispecv1.AnnotationTitle: "schema.json"})
	compositionBody, _ := json.Marshal(ocispecv1.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageManifest, ArtifactType: "application/vnd.example.pack.schema.v1+json", Config: empty, Layers: []ocispecv1.Descriptor{composition}})
	compositionMan := push(ocispecv1.MediaTypeImageManifest, compositionBody, nil)
	compositionMan.ArtifactType = "application/vnd.example.pack.schema.v1+json"
	packBody, _ := json.Marshal(ocispecv1.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispecv1.MediaTypeImageIndex, ArtifactType: "application/vnd.example.pack.v1+json",
		Manifests: []ocispecv1.Descriptor{compositionMan, comp}, Annotations: map[string]string{"io.example.name": "demo"}})
	pack := push(ocispecv1.MediaTypeImageIndex, packBody, map[string]string{"io.example.name": "demo"})
	if err := res.SetTag(ctx, pack); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(work, "out")
	dir, err := Extract(ctx, res, reference.Reference{Ref: "packs/demo:1.0"}, out,
		Options{Registry: "registry.example", Repo: "packs/demo", NameBy: "io.example.name", ImageConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "demo") {
		t.Fatalf("root dir: %s", dir)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "demo", "pack.yaml"))
	var pf packer.Pack
	yaml.Unmarshal(raw, &pf)
	if len(pf.Items) != 2 || pf.Items[0].From != "file://schema.json" || pf.Items[1].From != "cr://registry.example/packs/demo@"+comp.Digest.String() {
		t.Errorf("pack items: %+v", pf.Items)
	}
	raw, _ = os.ReadFile(filepath.Join(out, "alpine", "pack.yaml"))
	yaml.Unmarshal(raw, &pf)
	if len(pf.Items) != 2 || pf.Items[0].From != "cr://registry.example/packs/demo@"+img.Digest.String() || pf.Items[0].Platform != "linux/arm64" ||
		pf.Items[1].From != "file://schema.json" || pf.Items[1].Type != "application/vnd.example.schema.v1+json" {
		t.Errorf("component items: %+v", pf.Items)
	}
	cfgRaw, err := os.ReadFile(filepath.Join(out, "alpine", "image.json"))
	if err != nil || !bytes.Contains(cfgRaw, []byte(`"Cmd":["/bin/sh"]`)) {
		t.Errorf("image.json: %q %v", cfgRaw, err)
	}
	if _, err := os.Stat(filepath.Join(out, "alpine", "schema.json")); err != nil {
		t.Errorf("component schema: %v", err)
	}
}
