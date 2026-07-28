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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/klauspost/compress/gzip"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// gzipTar builds a gzip-compressed single-file tar layer, exactly as a registry
// would serve one.
func gzipTar(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(makeTar(t, name, content)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pushGzipLayers stores n gzip layers verbatim in a tar-mode layout and returns
// their descriptors alongside the file name each one carries.
func pushGzipLayers(t *testing.T, l *Layout, prefix string, n int) ([]ocispecv1.Descriptor, []string) {
	t.Helper()
	descs := make([]ocispecv1.Descriptor, 0, n)
	names := make([]string, 0, n)
	for i := range n {
		name := fmt.Sprintf("%s-%d.txt", prefix, i)
		descs = append(descs, pushBlob(t, l, gzipTar(t, name, name+" content"), ocispecv1.MediaTypeImageLayerGzip))
		names = append(names, name)
	}
	return descs, names
}

// pushIndex stores an OCI index over the given manifests and tags it.
func pushIndex(t *testing.T, l *Layout, manifests ...ocispecv1.Descriptor) ocispecv1.Descriptor {
	t.Helper()
	data, err := json.Marshal(ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: manifests,
	})
	if err != nil {
		t.Fatal(err)
	}

	desc := pushBlob(t, l, data, ocispecv1.MediaTypeImageIndex)
	if err = l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag index: %v", err)
	}
	return desc
}

// copyImage runs a parallel copy of the source layout's tagged image into dst.
func copyImage(t *testing.T, dst, src *Layout, concurrency int) ocispecv1.Descriptor {
	t.Helper()
	ctx := context.Background()

	desc, err := src.Resolve(ctx, reference.Reference{})
	if err != nil {
		t.Fatalf("Resolve src: %v", err)
	}

	if err = registry.Copy(ctx, dst, src, desc, registry.WithConcurrency(concurrency)); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("unpack requires chown privileges unavailable here: %v", err)
		}
		t.Fatalf("Copy: %v", err)
	}
	if err = dst.SetTag(ctx, desc); err != nil {
		t.Fatalf("SetTag dst: %v", err)
	}
	return desc
}

// TestCopy_ParallelTarToTar copies a many-layer image between two real layouts
// with every layer in flight at once and checks each blob arrived intact — the
// digest verification in writeBlob would catch bytes crossing between layers.
func TestCopy_ParallelTarToTar(t *testing.T) {
	src := newLayout(t)
	layers, _ := pushGzipLayers(t, src, "app", 8)
	manifest := pushManifest(t, src, layers)

	dst := newLayoutInDir(t, t.TempDir())
	copyImage(t, dst, src, len(layers))

	for i, layer := range layers {
		want, err := os.ReadFile(src.getBlobPath(layer.Digest))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst.getBlobPath(layer.Digest))
		if err != nil {
			t.Fatalf("layer[%d] missing in destination: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("layer[%d] %s: content differs after parallel copy", i, layer.Digest)
		}
	}

	if _, err := os.Stat(dst.getBlobPath(manifest.Digest)); err != nil {
		t.Errorf("manifest missing in destination: %v", err)
	}
	if err := dst.VerifyLayers(context.Background(), reference.Reference{}); err != nil {
		t.Errorf("VerifyLayers on the copy: %v", err)
	}
}

// TestCopy_ParallelTarToUnpack repacks into an unpack-mode layout with all
// layers extracting concurrently, the case where each layer is a separate
// directory tree being written at the same time.
func TestCopy_ParallelTarToUnpack(t *testing.T) {
	src := newLayout(t)
	layers, names := pushGzipLayers(t, src, "app", 6)
	pushManifest(t, src, layers)

	dst := newUnpackLayout(t)
	copyImage(t, dst, src, len(layers))

	dirs, err := dst.LayerDirs(context.Background(), reference.Reference{})
	if err != nil {
		t.Fatalf("dst.LayerDirs: %v", err)
	}
	if len(dirs) != len(layers) {
		t.Fatalf("got %d layer dirs, want %d", len(dirs), len(layers))
	}
	for i, dir := range dirs {
		if _, err = os.Stat(filepath.Join(dir, names[i])); err != nil {
			t.Errorf("layer[%d] not unpacked: %v", i, err)
		}
	}
}

// TestCopy_ParallelSharedLayerAcrossManifests is the case parallelism makes
// dangerous: two manifests of one index listing the same layer, so without
// per-digest deduplication two goroutines would extract into the same directory
// at the same time.
func TestCopy_ParallelSharedLayerAcrossManifests(t *testing.T) {
	src := newLayout(t)

	sharedBytes := gzipTar(t, "shared.txt", "shared content")
	shared := pushBlob(t, src, sharedBytes, ocispecv1.MediaTypeImageLayerGzip)

	amd64Layers, _ := pushGzipLayers(t, src, "amd64", 3)
	arm64Layers, _ := pushGzipLayers(t, src, "arm64", 3)

	config := pushImageConfig(t, src)
	amd64 := pushManifestWithConfig(t, src, config, append(amd64Layers, shared))
	arm64 := pushManifestWithConfig(t, src, config, append(arm64Layers, shared))
	pushIndex(t, src, amd64, arm64)

	dst := newUnpackLayout(t)
	copyImage(t, dst, src, 8)

	sharedDir := dst.getBlobPath(shared.Digest)
	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		t.Fatalf("shared layer not unpacked: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "shared.txt" {
		t.Errorf("shared layer directory holds %v, want exactly [shared.txt]", entries)
	}

	// Every distinct blob of both manifests must have made it across.
	for _, desc := range append(append(amd64Layers, arm64Layers...), config, amd64, arm64) {
		if _, err = os.Stat(dst.getBlobPath(desc.Digest)); err != nil {
			t.Errorf("blob %s missing in destination: %v", desc.Digest, err)
		}
	}
}

// TestUnpackLayer_PartialExtractionRemoved checks a layer that dies halfway
// through extraction leaves no directory behind. exists() counts any directory
// as a finished layer, so a leftover would be silently trusted on the next copy
// — the layer would stay truncated forever.
func TestUnpackLayer_PartialExtractionRemoved(t *testing.T) {
	ctx := context.Background()
	l := newUnpackLayout(t)

	content := strings.Repeat("payload", 200_000)
	full := gzipTar(t, "big.txt", content)
	desc := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(full),
		Size:      int64(len(full)),
	}

	// Feed half the compressed stream: the tar entry starts extracting and then
	// runs into an unexpected EOF, exactly like a connection dropped mid-layer.
	err := l.Push(ctx, desc, bytes.NewReader(full[:len(full)/2]))
	if err == nil {
		t.Fatal("Push of a truncated layer should fail")
	}
	if errors.Is(err, os.ErrPermission) {
		t.Skipf("unpack requires chown privileges unavailable here: %v", err)
	}

	if _, err = os.Stat(l.getBlobPath(desc.Digest)); !os.IsNotExist(err) {
		t.Fatalf("partial layer directory survived a failed unpack (stat err: %v)", err)
	}

	// Retrying with the whole layer must genuinely extract it. Had the partial
	// directory survived, exists() would report the layer as already present and
	// this push would be skipped.
	if err = l.Push(ctx, desc, bytes.NewReader(full)); err != nil {
		t.Fatalf("re-push after a failed unpack: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(l.getBlobPath(desc.Digest), "big.txt"))
	if err != nil {
		t.Fatalf("layer not extracted on retry: %v", err)
	}
	if string(got) != content {
		t.Errorf("retried layer holds %d bytes, want %d", len(got), len(content))
	}
}

// TestWriteBlob_PartialWriteRemoved checks the same for tar mode: a blob whose
// content does not match its digest must not be left on disk.
func TestWriteBlob_PartialWriteRemoved(t *testing.T) {
	l := newLayout(t)

	data := []byte("real content")
	desc := ocispecv1.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes([]byte("something else entirely")),
		Size:      int64(len(data)),
	}

	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err == nil {
		t.Fatal("Push with a mismatched digest should fail")
	}

	if _, err := os.Stat(l.getBlobPath(desc.Digest)); !os.IsNotExist(err) {
		t.Errorf("blob with a mismatched digest survived (stat err: %v)", err)
	}
}
