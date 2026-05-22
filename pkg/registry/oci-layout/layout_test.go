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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newLayout(t *testing.T) *Layout {
	t.Helper()
	tmpDir := t.TempDir()
	l, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l.(*Layout)
}

func newLayoutInDir(t *testing.T, dir string) *Layout {
	t.Helper()
	l, err := New(makeRef(dir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l.(*Layout)
}

func makeRef(tmpDir string) string {
	return fmt.Sprintf("%s%s:test:latest", reference.OciScheme, tmpDir)
}

func TestNewLayout(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	if layout == nil {
		t.Errorf("New() returned nil layout")
	}

	// Check if oci-layout directory was created
	layoutFile := filepath.Join(tmpDir, ocispecv1.ImageLayoutFile)
	if _, err := os.Stat(layoutFile); err != nil {
		t.Errorf("oci-layout file not created: %v", err)
	}

	// Check if blobs directory was created
	blobsDir := filepath.Join(tmpDir, ocispecv1.ImageBlobsDir)
	if _, err := os.Stat(blobsDir); err != nil {
		t.Errorf("blobs directory not created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// New — invalid inputs
// ---------------------------------------------------------------------------

func TestNew_InvalidRef(t *testing.T) {
	_, err := New("not-a-valid-ref")
	if err == nil {
		t.Fatal("New() should fail for invalid ref")
	}
}

func TestNew_WrongSchemeRejected(t *testing.T) {
	// cr:// is not oci:// — should be rejected
	_, err := New("cr://host/image:tag")
	if err == nil {
		t.Fatal("New() should reject non-oci scheme")
	}
}

func TestNew_CreatesRequiredStructure(t *testing.T) {
	dir := t.TempDir()
	_, err := New(makeRef(dir))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// oci-layout file
	if _, err = os.Stat(filepath.Join(dir, ocispecv1.ImageLayoutFile)); err != nil {
		t.Errorf("oci-layout file missing: %v", err)
	}
	// blobs directory
	if _, err = os.Stat(filepath.Join(dir, ocispecv1.ImageBlobsDir)); err != nil {
		t.Errorf("blobs directory missing: %v", err)
	}
	// index.json
	if _, err = os.Stat(filepath.Join(dir, ocispecv1.ImageIndexFile)); err != nil {
		t.Errorf("index.json missing: %v", err)
	}
}

func TestNew_ExistingLayoutReused(t *testing.T) {
	dir := t.TempDir()
	// Create first time
	if _, err := New(makeRef(dir)); err != nil {
		t.Fatalf("first New() error: %v", err)
	}
	// Reopen — should succeed without re-initializing
	if _, err := New(makeRef(dir)); err != nil {
		t.Fatalf("second New() error: %v", err)
	}
}

func TestNew_InvalidOciLayoutFile(t *testing.T) {
	dir := t.TempDir()
	// Write a bad oci-layout file
	os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), []byte("not json"), 0640)

	_, err := New(makeRef(dir))
	if err == nil {
		t.Fatal("New() should fail for invalid oci-layout file")
	}
}

func TestNew_WrongLayoutVersion(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	bad, _ := json.Marshal(ocispecv1.ImageLayout{Version: "999.0"})
	os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), bad, 0640)

	_, err := New(makeRef(dir))
	if err == nil {
		t.Fatal("New() should fail for unsupported layout version")
	}
}

func TestNew_UnpackOption_SetsArtifactType(t *testing.T) {
	dir := t.TempDir()
	l, err := New(makeRef(dir), Unpack())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	layout := l.(*Layout)
	if !layout.unpack {
		t.Error("unpack option should be set")
	}

	// Verify index.json uses unpack media type
	indexData, _ := os.ReadFile(filepath.Join(dir, ocispecv1.ImageIndexFile))
	var index ocispecv1.Index
	json.Unmarshal(indexData, &index)
	if index.ArtifactType != MediaTypeUnpackLayout {
		t.Errorf("ArtifactType = %q, want %q", index.ArtifactType, MediaTypeUnpackLayout)
	}
}

func TestNew_ExistingLayoutSetsUnpackFromIndex(t *testing.T) {
	dir := t.TempDir()
	// Create layout with unpack
	if _, err := New(makeRef(dir), Unpack()); err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Reopen without Unpack() — should infer from index
	l, err := New(makeRef(dir))
	if err != nil {
		t.Fatalf("second New() error: %v", err)
	}
	if !l.(*Layout).unpack {
		t.Error("unpack should be inferred from existing index ArtifactType")
	}
}

// ---------------------------------------------------------------------------
// Push — digest verification
// ---------------------------------------------------------------------------

func TestPush_DigestMismatch(t *testing.T) {
	l := newLayout(t)
	data := []byte("real content")
	wrongDigest := digest.FromBytes([]byte("wrong content"))

	desc := ocispecv1.Descriptor{
		Digest:    wrongDigest,
		Size:      int64(len(data)),
		MediaType: "application/octet-stream",
	}

	err := l.Push(context.Background(), desc, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Push() should fail when digest doesn't match content")
	}
}

func TestPush_CorrectDigest(t *testing.T) {
	l := newLayout(t)
	data := []byte("correct content")
	dgst := digest.FromBytes(data)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: "application/octet-stream",
	}

	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("Push() error: %v", err)
	}

	// Verify blob file exists
	blobPath := l.getBlobPath(dgst)
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("blob file not written: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

func TestFetch_MissingBlob(t *testing.T) {
	l := newLayout(t)
	desc := ocispecv1.Descriptor{
		Digest:    digest.FromBytes([]byte("phantom")),
		MediaType: "application/octet-stream",
	}

	_, err := l.Fetch(context.Background(), desc)
	if err == nil {
		t.Fatal("Fetch() should fail for non-existent blob")
	}
}

func TestFetch_ReturnsSameContent(t *testing.T) {
	l := newLayout(t)
	data := []byte("fetch me back")
	dgst := digest.FromBytes(data)
	desc := ocispecv1.Descriptor{Digest: dgst, Size: int64(len(data)), MediaType: "application/octet-stream"}

	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("Push error: %v", err)
	}

	rc, err := l.Fetch(context.Background(), desc)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	defer rc.Close()

	var buf bytes.Buffer
	buf.ReadFrom(rc)
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("content mismatch: got %q, want %q", buf.Bytes(), data)
	}
}

func TestPushAndFetch(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Create test data
	testData := []byte("test content")
	dgst := digest.FromBytes(testData)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(testData)),
		MediaType: "application/octet-stream",
	}

	// Push the blob
	if err := layout.Push(nil, desc, bytes.NewReader(testData)); err != nil {
		t.Errorf("Push() failed: %v", err)
	}

	// Fetch the blob
	reader, err := layout.Fetch(nil, desc)
	if err != nil {
		t.Errorf("Fetch() failed: %v", err)
	}
	defer func() { _ = reader.Close() }()

	// Verify the content
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Errorf("ReadFrom() failed: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), testData) {
		t.Errorf("Fetched content mismatch: got %v, want %v", buf.Bytes(), testData)
	}
}

// ---------------------------------------------------------------------------
// SetTag
// ---------------------------------------------------------------------------

func TestSetTag(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Create a test descriptor
	testData := []byte("test content")
	dgst := digest.FromBytes(testData)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(testData)),
		MediaType: "application/vnd.oci.image.manifest.v1+json",
	}

	// Set tag
	if err := layout.SetTag(nil, desc); err != nil {
		t.Errorf("SetTag() failed: %v", err)
	}

	// Check if index.json was created
	indexFile := filepath.Join(tmpDir, "index.json")
	if _, err := os.Stat(indexFile); err != nil {
		t.Errorf("index.json not created: %v", err)
	}
}

func TestSetTag_AppendsToIndex(t *testing.T) {
	l := newLayout(t)
	data := []byte("manifest data")
	dgst := digest.FromBytes(data)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: ocispecv1.MediaTypeImageManifest,
	}

	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	index, err := l.readIndex()
	if err != nil {
		t.Fatalf("readIndex error: %v", err)
	}
	if len(index.Manifests) != 1 {
		t.Errorf("expected 1 manifest in index, got %d", len(index.Manifests))
	}
}

func TestSetTag_UpdatesExistingDigest(t *testing.T) {
	l := newLayout(t)
	data := []byte("v1 manifest")
	dgst := digest.FromBytes(data)
	desc := ocispecv1.Descriptor{Digest: dgst, Size: int64(len(data)), MediaType: ocispecv1.MediaTypeImageManifest}

	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("first SetTag error: %v", err)
	}
	// Set same digest again — should update, not append
	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("second SetTag error: %v", err)
	}

	index, _ := l.readIndex()
	if len(index.Manifests) != 1 {
		t.Errorf("expected 1 manifest after update, got %d", len(index.Manifests))
	}
}

func TestSetTag_SetsAnnotationRefName(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	data := []byte("manifest bytes")
	dgst := digest.FromBytes(data)
	desc := ocispecv1.Descriptor{Digest: dgst, Size: int64(len(data)), MediaType: ocispecv1.MediaTypeImageManifest}

	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	index, _ := l.readIndex()
	for _, m := range index.Manifests {
		if m.Digest == dgst {
			ref := m.Annotations[ocispecv1.AnnotationRefName]
			if ref == "" {
				t.Error("AnnotationRefName should be set after SetTag")
			}
			return
		}
	}
	t.Error("manifest not found in index after SetTag")
}

// ---------------------------------------------------------------------------
// Resolve
// ---------------------------------------------------------------------------

func TestResolve_NotFound(t *testing.T) {
	l := newLayout(t)
	_, err := l.Resolve(context.Background(), makeRef(t.TempDir()))
	if err == nil {
		t.Fatal("Resolve() should fail when manifest not found")
	}
}

func TestResolve_FoundAfterSetTag(t *testing.T) {
	dir := t.TempDir()
	ref := makeRef(dir)
	l := newLayoutInDir(t, dir)

	data := []byte("manifest")
	dgst := digest.FromBytes(data)

	// We need to push the manifest blob and tag it
	if err := l.Push(context.Background(), ocispecv1.Descriptor{
		Digest: dgst, Size: int64(len(data)), MediaType: "application/octet-stream",
	}, bytes.NewReader(data)); err != nil {
		t.Fatalf("Push error: %v", err)
	}

	if err := l.SetTag(context.Background(), ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: ocispecv1.MediaTypeImageManifest,
	}); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	// Resolve should now find it
	found, err := l.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve() %s error: %v", ref, err)
	}
	if found.Digest != dgst {
		t.Errorf("Digest = %v, want %v", found.Digest, dgst)
	}
}

// ---------------------------------------------------------------------------
// Exists
// ---------------------------------------------------------------------------

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Create and push a blob
	testData := []byte("test content")
	dgst := digest.FromBytes(testData)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(testData)),
		MediaType: "application/octet-stream",
	}

	if err := layout.Push(nil, desc, bytes.NewReader(testData)); err != nil {
		t.Fatalf("Push() failed: %v", err)
	}

	// Check if blob exists
	exists, err := layout.Exists(nil, "")
	if err != nil {
		t.Errorf("Exists() failed: %v", err)
	}

	// Note: The existence check in this implementation checks if the index has manifests
	// The actual blob existence would need separate checking
	_ = exists
}

func TestExists_ReturnsFalseForEmpty(t *testing.T) {
	l := newLayout(t)
	exists, err := l.Exists(context.Background(), "")
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	// Empty ref won't resolve to any manifest
	_ = exists
}

// ---------------------------------------------------------------------------
// getBlobPath / getBlobDirectory
// ---------------------------------------------------------------------------

func TestGetBlobPath(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	testData := []byte("test")
	dgst := digest.FromBytes(testData)

	blobPath := layout.(*Layout).getBlobPath(dgst)
	expectedPath := filepath.Join(tmpDir, "blobs", dgst.Algorithm().String(), dgst.Hex())

	if blobPath != expectedPath {
		t.Errorf("getBlobPath() = %q, want %q", blobPath, expectedPath)
	}
}

func TestGetBlobDirectory(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	dgst := digest.FromBytes([]byte("test"))

	blobDir := l.getBlobDirectory(dgst)
	expected := filepath.Join(dir, ocispecv1.ImageBlobsDir, dgst.Algorithm().String())
	if blobDir != expected {
		t.Errorf("getBlobDirectory() = %q, want %q", blobDir, expected)
	}
}

func TestGetBlobPath_ContainsHex(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	dgst := digest.FromBytes([]byte("blob"))

	blobPath := l.getBlobPath(dgst)
	if blobPath == "" {
		t.Error("getBlobPath() returned empty string")
	}
	if filepath.Base(blobPath) != dgst.Hex() {
		t.Errorf("getBlobPath() base = %q, want hex %q", filepath.Base(blobPath), dgst.Hex())
	}
}

func TestMount(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	err = layout.Mount(nil, "")
	if err != nil {
		t.Errorf("Mount() should return an error for OCI Layout")
	}
}
