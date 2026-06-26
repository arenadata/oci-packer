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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/klauspost/compress/gzip"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestCopy_TarToUnpack copies an image from a tar-mode layout into an
// unpack-mode layout (the `copy --unpack oci:// oci://` repack flow) and
// verifies the destination ends up with unpacked layer directories that
// LayerDirs accepts.
func TestCopy_TarToUnpack(t *testing.T) {
	ctx := context.Background()

	// Source: tar-mode layout with one uncompressed tar layer.
	src := newLayout(t)
	tarBytes := makeTar(t, "hello.txt", "hi")
	layer := pushBlob(t, src, tarBytes, ocispecv1.MediaTypeImageLayer)
	manifest := pushManifest(t, src, []ocispecv1.Descriptor{layer})

	// Destination: unpack-mode layout.
	dst := newUnpackLayout(t)

	desc, err := src.Resolve(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("Resolve src: %v", err)
	}

	if err := registry.Copy(ctx, dst, src, desc); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("unpack requires chown privileges unavailable here: %v", err)
		}
		t.Fatalf("Copy: %v", err)
	}
	if err := dst.SetTag(ctx, manifest); err != nil {
		t.Fatalf("SetTag dst: %v", err)
	}

	// The destination must now be a mountable, unpack-mode image.
	dirs, err := dst.LayerDirs(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("dst.LayerDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 layer dir, got %d", len(dirs))
	}
	if fi, err := os.Stat(dirs[0]); err != nil || !fi.IsDir() {
		t.Fatalf("layer is not an unpacked directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "hello.txt")); err != nil {
		t.Errorf("unpacked layer missing file: %v", err)
	}
}

// TestCopy_TarToUnpack_GzipLayer covers the realistic case where the source
// tar-mode layout holds a gzip-compressed layer (as registries serve). The
// destination must decompress and unpack it.
func TestCopy_TarToUnpack_GzipLayer(t *testing.T) {
	ctx := context.Background()

	src := newLayout(t)

	// gzip-compress a tar layer and store it verbatim in the tar-mode layout.
	tarBytes := makeTar(t, "data.txt", "payload")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(tarBytes); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	gz := buf.Bytes()

	layer := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(gz),
		Size:      int64(len(gz)),
	}
	if err := src.Push(ctx, layer, bytes.NewReader(gz)); err != nil {
		t.Fatalf("push gzip layer into tar layout: %v", err)
	}
	manifest := pushManifest(t, src, []ocispecv1.Descriptor{layer})

	dst := newUnpackLayout(t)

	desc, err := src.Resolve(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("Resolve src: %v", err)
	}

	if err := registry.Copy(ctx, dst, src, desc); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("unpack requires chown privileges unavailable here: %v", err)
		}
		t.Fatalf("Copy: %v", err)
	}
	if err := dst.SetTag(ctx, manifest); err != nil {
		t.Fatalf("SetTag dst: %v", err)
	}

	dirs, err := dst.LayerDirs(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("dst.LayerDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 layer dir, got %d", len(dirs))
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "data.txt")); err != nil {
		t.Errorf("gzip layer not unpacked: %v", err)
	}
}
