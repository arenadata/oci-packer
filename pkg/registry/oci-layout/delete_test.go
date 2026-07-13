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
	"os"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// layoutForTag returns a view of l addressing a specific tag, mirroring how the
// CLI opens a layout with a concrete <repo>:<tag> reference. SetTag/Delete use
// l.ref.Ref as the image name, so this lets a single layout hold several
// distinctly-tagged images in one test.
func layoutForTag(l *Layout, tag string) *Layout {
	ref := l.ref
	ref.Ref = tag
	return &Layout{ref: ref, unpack: l.unpack}
}

func blobExists(t *testing.T, l *Layout, d digest.Digest) bool {
	t.Helper()
	_, err := os.Stat(l.getBlobPath(d))
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat blob %s: %v", d, err)
	return false
}

func indexRefNames(t *testing.T, l *Layout) []string {
	t.Helper()
	index, err := l.readIndex()
	if err != nil {
		t.Fatalf("readIndex: %v", err)
	}
	names := make([]string, 0, len(index.Manifests))
	for _, m := range index.Manifests {
		names = append(names, m.Annotations[ocispecv1.AnnotationRefName])
	}
	return names
}

// ---------------------------------------------------------------------------
// Delete — single image
// ---------------------------------------------------------------------------

func TestDelete_SingleImage_RemovesEntryAndBlobs(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfg := pushBlob(t, l, []byte(`{"config":"a"}`), ocispecv1.MediaTypeImageConfig)
	layer1 := pushBlob(t, l, []byte("layer-one"), ocispecv1.MediaTypeImageLayer)
	layer2 := pushBlob(t, l, []byte("layer-two"), ocispecv1.MediaTypeImageLayer)
	m := pushManifestWithConfig(t, l, cfg, []ocispecv1.Descriptor{layer1, layer2})
	if err := layoutForTag(l, "app:v1").SetTag(ctx, m); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	if err := layoutForTag(l, "app:v1").Delete(ctx, reference.Reference{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if names := indexRefNames(t, l); len(names) != 0 {
		t.Fatalf("expected empty index, got %v", names)
	}
	for _, d := range []digest.Digest{m.Digest, cfg.Digest, layer1.Digest, layer2.Digest} {
		if blobExists(t, l, d) {
			t.Errorf("blob %s should have been removed", d)
		}
	}
}

// ---------------------------------------------------------------------------
// Delete — shared layers are retained
// ---------------------------------------------------------------------------

func TestDelete_SharedLayerRetained(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfg1 := pushBlob(t, l, []byte(`{"c":1}`), ocispecv1.MediaTypeImageConfig)
	cfg2 := pushBlob(t, l, []byte(`{"c":2}`), ocispecv1.MediaTypeImageConfig)
	shared := pushBlob(t, l, []byte("shared-layer"), ocispecv1.MediaTypeImageLayer)
	onlyA := pushBlob(t, l, []byte("layer-a"), ocispecv1.MediaTypeImageLayer)
	onlyB := pushBlob(t, l, []byte("layer-b"), ocispecv1.MediaTypeImageLayer)

	m1 := pushManifestWithConfig(t, l, cfg1, []ocispecv1.Descriptor{shared, onlyA})
	m2 := pushManifestWithConfig(t, l, cfg2, []ocispecv1.Descriptor{shared, onlyB})
	if err := layoutForTag(l, "app:v1").SetTag(ctx, m1); err != nil {
		t.Fatalf("SetTag v1: %v", err)
	}
	if err := layoutForTag(l, "app:v2").SetTag(ctx, m2); err != nil {
		t.Fatalf("SetTag v2: %v", err)
	}

	if err := layoutForTag(l, "app:v1").Delete(ctx, reference.Reference{}); err != nil {
		t.Fatalf("Delete v1: %v", err)
	}

	// v1-exclusive blobs gone.
	for _, d := range []digest.Digest{m1.Digest, cfg1.Digest, onlyA.Digest} {
		if blobExists(t, l, d) {
			t.Errorf("v1-exclusive blob %s should have been removed", d)
		}
	}
	// Shared layer and v2's blobs retained.
	for _, d := range []digest.Digest{shared.Digest, cfg2.Digest, onlyB.Digest, m2.Digest} {
		if !blobExists(t, l, d) {
			t.Errorf("blob %s should have been retained", d)
		}
	}

	if names := indexRefNames(t, l); len(names) != 1 || names[0] != "app:v2" {
		t.Fatalf("expected only app:v2 in index, got %v", names)
	}

	// v2 must still resolve and its layers remain fetchable.
	if _, err := layoutForTag(l, "app:v2").Resolve(ctx, reference.Reference{}); err != nil {
		t.Errorf("app:v2 should still resolve: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Delete — multi-platform index
// ---------------------------------------------------------------------------

func TestDelete_IndexRemovesAllSubManifests(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfgA := pushBlob(t, l, []byte(`{"p":"a"}`), ocispecv1.MediaTypeImageConfig)
	layerA := pushBlob(t, l, []byte("amd64-layer"), ocispecv1.MediaTypeImageLayer)
	manA := pushManifestWithConfig(t, l, cfgA, []ocispecv1.Descriptor{layerA})

	cfgB := pushBlob(t, l, []byte(`{"p":"b"}`), ocispecv1.MediaTypeImageConfig)
	layerB := pushBlob(t, l, []byte("arm64-layer"), ocispecv1.MediaTypeImageLayer)
	manB := pushManifestWithConfig(t, l, cfgB, []ocispecv1.Descriptor{layerB})

	idx := ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{manA, manB},
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	indexDesc := pushBlob(t, l, data, ocispecv1.MediaTypeImageIndex)
	if err := layoutForTag(l, "multi:v1").SetTag(ctx, indexDesc); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	if err := layoutForTag(l, "multi:v1").Delete(ctx, reference.Reference{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	all := []digest.Digest{
		indexDesc.Digest,
		manA.Digest, cfgA.Digest, layerA.Digest,
		manB.Digest, cfgB.Digest, layerB.Digest,
	}
	for _, d := range all {
		if blobExists(t, l, d) {
			t.Errorf("blob %s should have been removed with the index", d)
		}
	}
	if names := indexRefNames(t, l); len(names) != 0 {
		t.Fatalf("expected empty index, got %v", names)
	}
}

// ---------------------------------------------------------------------------
// Delete — errors
// ---------------------------------------------------------------------------

func TestDelete_NotFound(t *testing.T) {
	l := newLayout(t)
	ctx := context.Background()

	cfg := pushImageConfig(t, l)
	m := pushManifestWithConfig(t, l, cfg, nil)
	if err := layoutForTag(l, "app:v1").SetTag(ctx, m); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	if err := layoutForTag(l, "app:does-not-exist").Delete(ctx, reference.Reference{}); err == nil {
		t.Fatal("expected error deleting a missing tag")
	}

	// The existing image must be untouched.
	if names := indexRefNames(t, l); len(names) != 1 || names[0] != "app:v1" {
		t.Fatalf("index should be unchanged, got %v", names)
	}
	if !blobExists(t, l, m.Digest) {
		t.Error("manifest blob should still exist after a failed delete")
	}
}

// ---------------------------------------------------------------------------
// Delete — unpack mode (layer directories)
// ---------------------------------------------------------------------------

func TestDelete_UnpackMode_RemovesLayerDirs(t *testing.T) {
	l := newUnpackLayout(t)
	ctx := context.Background()

	d := pushUnpackedLayer(t, l, makeTar(t, "f.txt", "content"))
	m := pushManifest(t, l, []ocispecv1.Descriptor{d}) // tags as l.ref ("test:latest")

	// Sanity: the layer was unpacked into a directory.
	if st, err := os.Stat(l.getBlobPath(d.Digest)); err != nil || !st.IsDir() {
		t.Fatalf("expected unpacked layer directory: err=%v", err)
	}

	if err := l.Delete(ctx, reference.Reference{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if blobExists(t, l, d.Digest) {
		t.Error("unpacked layer directory should have been removed")
	}
	if blobExists(t, l, m.Digest) {
		t.Error("manifest blob should have been removed")
	}
}

// ---------------------------------------------------------------------------
// mount-detection guard
// ---------------------------------------------------------------------------

func TestMountedOrphans_NoOverlayMatch(t *testing.T) {
	// On any platform, a fresh layout's blobs are not mounted, so a delete must
	// not be blocked by the mount check. (On non-Linux mountedLowerDirs simply
	// returns an empty set.)
	l := newUnpackLayout(t)
	orphans := []digest.Digest{digest.FromBytes([]byte("nope"))}
	mounted, err := l.mountedOrphans(orphans)
	if err != nil {
		t.Fatalf("mountedOrphans: %v", err)
	}
	if len(mounted) != 0 {
		t.Fatalf("expected no mounted orphans, got %v", mounted)
	}
}
