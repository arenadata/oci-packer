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
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newUnpackLayout(t *testing.T) *Layout {
	t.Helper()
	dir := t.TempDir()
	l, err := New(makeRef(dir).String(), Unpack())
	if err != nil {
		t.Fatalf("New(Unpack): %v", err)
	}
	return l.(*Layout)
}

func makeTar(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Use the current uid/gid so archive.Unpack's chown succeeds for an
	// unprivileged test runner.
	hdr := &tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(content)),
		Uid:  os.Getuid(),
		Gid:  os.Getgid(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pushBlob makes sure the layout holds these bytes and returns their descriptor.
//
// Pushing content the layout already has is not a failure: blobs are addressed
// by digest, so two fixtures that happen to build the same bytes — two manifests
// sharing one image config, say — describe the same blob, and the second push is
// a no-op the layout reports as ErrAlreadyExists.
func pushBlob(t *testing.T, l *Layout, data []byte, mt string) ocispecv1.Descriptor {
	t.Helper()
	desc := ocispecv1.Descriptor{
		MediaType: mt,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil && !registry.IsAlreadyExists(err) {
		t.Fatalf("Push blob: %v", err)
	}
	return desc
}

// pushUnpackedLayer pushes a tar layer into an unpack-mode layout. Unpacking
// chowns extracted files; on environments without that privilege (e.g. an
// unprivileged macOS runner) the test is skipped, since mount itself is
// Linux/root-only anyway.
func pushUnpackedLayer(t *testing.T, l *Layout, tarBytes []byte) ocispecv1.Descriptor {
	t.Helper()
	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageLayer,
		Digest:    digest.FromBytes(tarBytes),
		Size:      int64(len(tarBytes)),
	}
	if err := l.Push(context.Background(), desc, bytes.NewReader(tarBytes)); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("unpack requires chown privileges unavailable here: %v", err)
		}
		t.Fatalf("Push layer: %v", err)
	}
	return desc
}

// pushImageConfig pushes a minimal OCI image config blob.
func pushImageConfig(t *testing.T, l *Layout) ocispecv1.Descriptor {
	t.Helper()
	cfg := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	return pushBlob(t, l, cfg, ocispecv1.MediaTypeImageConfig)
}

// pushManifest builds an image manifest from the given layers, pushes it and
// tags it.
func pushManifest(t *testing.T, l *Layout, layers []ocispecv1.Descriptor) ocispecv1.Descriptor {
	t.Helper()
	desc := pushManifestWithConfig(t, l, pushImageConfig(t, l), layers)
	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag: %v", err)
	}
	return desc
}

// pushManifestWithConfig builds and pushes a manifest with an explicit config
// descriptor (without tagging).
func pushManifestWithConfig(t *testing.T, l *Layout, config ocispecv1.Descriptor, layers []ocispecv1.Descriptor) ocispecv1.Descriptor {
	t.Helper()
	m := ocispecv1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageManifest,
		Config:    config,
		Layers:    layers,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return pushBlob(t, l, data, ocispecv1.MediaTypeImageManifest)
}

// ---------------------------------------------------------------------------
// LayerDirs
// ---------------------------------------------------------------------------

func TestLayerDirs_UnpackMode(t *testing.T) {
	l := newUnpackLayout(t)

	tar1 := makeTar(t, "a.txt", "first")
	tar2 := makeTar(t, "b.txt", "second")
	d1 := pushUnpackedLayer(t, l, tar1)
	d2 := pushUnpackedLayer(t, l, tar2)
	pushManifest(t, l, []ocispecv1.Descriptor{d1, d2})

	dirs, err := l.LayerDirs(context.Background(), reference.Reference{})
	if err != nil {
		t.Fatalf("LayerDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d", len(dirs))
	}
	// bottom-to-top order preserved
	if dirs[0] != l.getBlobPath(d1.Digest) || dirs[1] != l.getBlobPath(d2.Digest) {
		t.Errorf("wrong order: %v", dirs)
	}
	// each is an unpacked directory containing the file
	if _, err := os.Stat(filepath.Join(dirs[0], "a.txt")); err != nil {
		t.Errorf("layer 0 not unpacked: %v", err)
	}
}

func TestLayerDirs_NonImageRejected(t *testing.T) {
	l := newUnpackLayout(t)

	// An arbitrary OCI artifact: empty (non-image) config, custom-typed layer.
	layer := pushBlob(t, l, []byte("some artifact payload"), "application/vnd.example.data")
	desc := pushManifestWithConfig(t, l, ocispecv1.DescriptorEmptyJSON, []ocispecv1.Descriptor{layer})
	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	_, err := l.LayerDirs(context.Background(), reference.Reference{})
	if err == nil {
		t.Fatal("expected non-image artifact to be rejected")
	}
}

func TestLayerDirs_TarModeFails(t *testing.T) {
	l := newLayout(t) // tar mode (no Unpack)

	data := []byte("not a real layer")
	d := pushBlob(t, l, data, ocispecv1.MediaTypeImageLayer)
	pushManifest(t, l, []ocispecv1.Descriptor{d})

	_, err := l.LayerDirs(context.Background(), reference.Reference{})
	if err == nil {
		t.Fatal("expected error in tar mode")
	}
}

func TestLayerDirs_IndexAutoSelectPlatform(t *testing.T) {
	l := newUnpackLayout(t)

	host := platforms.DefaultSpec()
	other := host
	other.Architecture = "ppc64le"
	if host.Architecture == "ppc64le" {
		other.Architecture = "s390x"
	}

	// Host manifest with a distinctive layer.
	hostTar := makeTar(t, "host.txt", "host-layer")
	hostLayer := pushUnpackedLayer(t, l, hostTar)
	hostManifest := pushManifestNoTag(t, l, []ocispecv1.Descriptor{hostLayer})

	// Other-platform manifest with a different layer.
	otherTar := makeTar(t, "other.txt", "other-layer")
	otherLayer := pushUnpackedLayer(t, l, otherTar)
	otherManifest := pushManifestNoTag(t, l, []ocispecv1.Descriptor{otherLayer})

	hostManifest.Platform = &host
	otherManifest.Platform = &other

	index := ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{otherManifest, hostManifest},
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	indexDesc := pushBlob(t, l, data, ocispecv1.MediaTypeImageIndex)
	if err := l.SetTag(context.Background(), indexDesc); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	dirs, err := l.LayerDirs(context.Background(), reference.Reference{})
	if err != nil {
		t.Fatalf("LayerDirs: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != l.getBlobPath(hostLayer.Digest) {
		t.Errorf("expected host layer selected, got %v", dirs)
	}
}

// pushManifestNoTag builds and pushes an image manifest without tagging it
// (used for index members).
func pushManifestNoTag(t *testing.T, l *Layout, layers []ocispecv1.Descriptor) ocispecv1.Descriptor {
	t.Helper()
	return pushManifestWithConfig(t, l, pushImageConfig(t, l), layers)
}

// ---------------------------------------------------------------------------
// VerifyLayers
// ---------------------------------------------------------------------------

func TestVerifyLayers_TarMode_OK(t *testing.T) {
	l := newLayout(t)

	d1 := pushBlob(t, l, []byte("layer one"), ocispecv1.MediaTypeImageLayer)
	d2 := pushBlob(t, l, []byte("layer two"), ocispecv1.MediaTypeImageLayer)
	pushManifest(t, l, []ocispecv1.Descriptor{d1, d2})

	if err := l.VerifyLayers(context.Background(), reference.Reference{}); err != nil {
		t.Fatalf("VerifyLayers: %v", err)
	}
}

func TestVerifyLayers_TarMode_Tampered(t *testing.T) {
	l := newLayout(t)

	d := pushBlob(t, l, []byte("original"), ocispecv1.MediaTypeImageLayer)
	pushManifest(t, l, []ocispecv1.Descriptor{d})

	// Overwrite the blob on disk with different content.
	if err := os.WriteFile(l.getBlobPath(d.Digest), []byte("tampered!"), 0640); err != nil {
		t.Fatal(err)
	}

	err := l.VerifyLayers(context.Background(), reference.Reference{})
	if err == nil {
		t.Fatal("expected digest mismatch error")
	}
}

func TestVerifyLayers_UnpackMode_Unsupported(t *testing.T) {
	l := newUnpackLayout(t)

	// VerifyLayers rejects unpack mode before touching any layer, so a plain
	// blob (no unpack/chown) is enough to set the scene.
	d := pushBlob(t, l, []byte("x"), "application/octet-stream")
	pushManifest(t, l, []ocispecv1.Descriptor{d})

	err := l.VerifyLayers(context.Background(), reference.Reference{})
	if err == nil {
		t.Fatal("expected unsupported error in unpack mode")
	}
}
