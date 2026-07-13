/*
  Copyright (c) 2026 Arenadata Softwer LLC.
  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package layout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList_Empty(t *testing.T) {
	l := newLayout(t)
	images, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("expected empty list, got %d", len(images))
	}
}

func TestList_ReportsEntries(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfg1 := pushBlob(t, l, []byte(`{"c":1}`), ocispecv1.MediaTypeImageConfig)
	m1 := pushManifestWithConfig(t, l, cfg1, []ocispecv1.Descriptor{
		pushBlob(t, l, []byte("layer"), ocispecv1.MediaTypeImageLayer),
	})
	if err := layoutForTag(l, "app:v1").SetTag(ctx, m1); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	images, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(images))
	}

	got := images[0]
	if got.Ref != "app:v1" {
		t.Errorf("Ref = %q, want app:v1", got.Ref)
	}
	if got.Digest != m1.Digest {
		t.Errorf("Digest = %q, want %q", got.Digest, m1.Digest)
	}
	if got.Kind != "image" {
		t.Errorf("Kind = %q, want image", got.Kind)
	}
	if got.Size != m1.Size {
		t.Errorf("Size = %d, want %d", got.Size, m1.Size)
	}
}

// ---------------------------------------------------------------------------
// PackComponents — single manifest
// ---------------------------------------------------------------------------

func TestPackComponents_SingleManifest(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfg := pushBlob(t, l, []byte(`{"c":1}`), ocispecv1.MediaTypeImageConfig)

	layer := pushBlob(t, l, []byte("payload"), ocispecv1.MediaTypeImageLayer)
	layer.Annotations = map[string]string{ocispecv1.AnnotationTitle: "app.tar"}

	m := pushManifestWithConfig(t, l, cfg, []ocispecv1.Descriptor{layer})
	if err := layoutForTag(l, "app:v1").SetTag(ctx, m); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	pack, err := layoutForTag(l, "app:v1").PackComponents(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("PackComponents: %v", err)
	}

	if pack.Ref != "app:v1" || pack.Kind != "image" {
		t.Errorf("unexpected pack header: ref=%q kind=%q", pack.Ref, pack.Kind)
	}
	if len(pack.Manifests) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(pack.Manifests))
	}

	comps := pack.Manifests[0].Components
	if len(comps) != 2 {
		t.Fatalf("expected config + 1 layer = 2 components, got %d", len(comps))
	}
	if comps[0].Role != "config" || comps[0].Digest != cfg.Digest {
		t.Errorf("component[0] = %+v, want config %s", comps[0], cfg.Digest)
	}
	if comps[1].Role != "layer" || comps[1].Title != "app.tar" {
		t.Errorf("component[1] = %+v, want layer titled app.tar", comps[1])
	}
	if comps[1].Digest != layer.Digest {
		t.Errorf("layer digest = %q, want %q", comps[1].Digest, layer.Digest)
	}
}

// ---------------------------------------------------------------------------
// PackComponents — multi-platform index
// ---------------------------------------------------------------------------

func TestPackComponents_Index(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	host := platforms.DefaultSpec()
	other := host
	other.Architecture = "ppc64le"
	if host.Architecture == "ppc64le" {
		other.Architecture = "s390x"
	}

	cfgA := pushBlob(t, l, []byte(`{"p":"a"}`), ocispecv1.MediaTypeImageConfig)
	manA := pushManifestWithConfig(t, l, cfgA, []ocispecv1.Descriptor{
		pushBlob(t, l, []byte("layer-a"), ocispecv1.MediaTypeImageLayer),
	})
	manA.Platform = &host

	cfgB := pushBlob(t, l, []byte(`{"p":"b"}`), ocispecv1.MediaTypeImageConfig)
	manB := pushManifestWithConfig(t, l, cfgB, []ocispecv1.Descriptor{
		pushBlob(t, l, []byte("layer-b"), ocispecv1.MediaTypeImageLayer),
	})
	manB.Platform = &other

	index := ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{manA, manB},
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	indexDesc := pushBlob(t, l, data, ocispecv1.MediaTypeImageIndex)
	if err := layoutForTag(l, "multi:v1").SetTag(ctx, indexDesc); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	pack, err := layoutForTag(l, "multi:v1").PackComponents(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("PackComponents: %v", err)
	}

	if pack.Kind != "index" {
		t.Errorf("Kind = %q, want index", pack.Kind)
	}
	if len(pack.Manifests) != 2 {
		t.Fatalf("expected 2 platform manifests, got %d", len(pack.Manifests))
	}

	wantPlatforms := map[string]bool{
		platforms.Format(host):  false,
		platforms.Format(other): false,
	}
	for _, m := range pack.Manifests {
		if _, ok := wantPlatforms[m.Platform]; !ok {
			t.Errorf("unexpected platform %q", m.Platform)
			continue
		}
		wantPlatforms[m.Platform] = true
		if len(m.Components) != 2 { // config + 1 layer
			t.Errorf("platform %q: expected 2 components, got %d", m.Platform, len(m.Components))
		}
	}
	for p, seen := range wantPlatforms {
		if !seen {
			t.Errorf("platform %q missing from output", p)
		}
	}
}

func TestPackComponents_NotFound(t *testing.T) {
	l := newLayout(t)
	_, err := layoutForTag(l, "nope:v1").PackComponents(context.Background(), reference.Reference{})
	if err == nil {
		t.Fatal("expected error for missing reference")
	}
}
