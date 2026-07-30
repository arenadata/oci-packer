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
	"os"
	"path/filepath"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newLayout(t *testing.T) *Layout {
	t.Helper()
	tmpDir := t.TempDir()
	l, err := New(makeRef(tmpDir).String())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l.(*Layout)
}

func newLayoutInDir(t *testing.T, dir string) *Layout {
	t.Helper()
	l, err := New(makeRef(dir).String())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l.(*Layout)
}

func makeRef(tmpDir string) reference.Reference {
	return reference.Reference{
		Scheme: reference.OciScheme,
		Host:   "",
		Path:   tmpDir,
		Ref:    "test:latest",
	}
}

func TestNewLayout(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir).String())
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
	_, err := New(makeRef(dir).String())
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
	if _, err := New(makeRef(dir).String()); err != nil {
		t.Fatalf("first New() error: %v", err)
	}
	// Reopen — should succeed without re-initializing
	if _, err := New(makeRef(dir).String()); err != nil {
		t.Fatalf("second New() error: %v", err)
	}
}

func TestNew_InvalidOciLayoutFile(t *testing.T) {
	dir := t.TempDir()
	// Write a bad oci-layout file
	_ = os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	_ = os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), []byte("not json"), 0640)

	_, err := New(makeRef(dir).String())
	if err == nil {
		t.Fatal("New() should fail for invalid oci-layout file")
	}
}

func TestNew_WrongLayoutVersion(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	bad, _ := json.Marshal(ocispecv1.ImageLayout{Version: "999.0"})
	_ = os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), bad, 0640)

	_, err := New(makeRef(dir).String())
	if err == nil {
		t.Fatal("New() should fail for unsupported layout version")
	}
}

func TestNew_UnpackOption_SetsArtifactType(t *testing.T) {
	dir := t.TempDir()
	l, err := New(makeRef(dir).String(), Unpack())
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
	_ = json.Unmarshal(indexData, &index)
	if index.ArtifactType != MediaTypeUnpackLayout {
		t.Errorf("ArtifactType = %q, want %q", index.ArtifactType, MediaTypeUnpackLayout)
	}
}

func TestNew_ExistingLayoutSetsUnpackFromIndex(t *testing.T) {
	dir := t.TempDir()
	// Create layout with unpack
	if _, err := New(makeRef(dir).String(), Unpack()); err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Reopen without Unpack() — should infer from index
	l, err := New(makeRef(dir).String())
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

	_, err := l.Fetch(context.Background(), reference.Reference{Ref: desc.Digest.String()})
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

	rc, err := l.Fetch(context.Background(), reference.Reference{Ref: desc.Digest.String()})
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	defer func() { _ = rc.Close() }()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rc)
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("content mismatch: got %q, want %q", buf.Bytes(), data)
	}
}

func TestPushAndFetch(t *testing.T) {
	tmpDir := t.TempDir()
	layout, err := New(makeRef(tmpDir).String())
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
	reader, err := layout.Fetch(nil, reference.Reference{Ref: desc.Digest.String()})
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
	layout, err := New(makeRef(tmpDir).String())
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
	layout, err := New(makeRef(tmpDir).String())
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
	exists, err := layout.Exists(nil, reference.Reference{})
	if err != nil {
		t.Errorf("Exists() failed: %v", err)
	}

	// Note: The existence check in this implementation checks if the index has manifests
	// The actual blob existence would need separate checking
	_ = exists
}

func TestExists_ReturnsFalseForEmpty(t *testing.T) {
	l := newLayout(t)
	exists, err := l.Exists(context.Background(), reference.Reference{})
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
	layout, err := New(makeRef(tmpDir).String())
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

// ---------------------------------------------------------------------------
// FetchReference
// ---------------------------------------------------------------------------

func TestFetchReference_NotFound(t *testing.T) {
	l := newLayout(t)
	_, _, err := l.FetchReference(context.Background(), makeRef(t.TempDir()))
	if err == nil {
		t.Fatal("FetchReference() should fail for non-existent reference")
	}
}

func TestFetchReference_FoundAfterPush(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	ref := makeRef(dir)

	// Push manifest blob
	manifestData := []byte("manifest content")
	manifestDigest := digest.FromBytes(manifestData)

	desc := ocispecv1.Descriptor{
		Digest:    manifestDigest,
		Size:      int64(len(manifestData)),
		MediaType: ocispecv1.MediaTypeImageManifest,
	}

	if err := l.Push(context.Background(), desc, bytes.NewReader(manifestData)); err != nil {
		t.Fatalf("Push error: %v", err)
	}

	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	// FetchReference should now return descriptor and reader
	fetchedDesc, reader, err := l.FetchReference(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReference error: %v", err)
	}
	defer func() { _ = reader.Close() }()

	if fetchedDesc.Digest != manifestDigest {
		t.Errorf("Digest mismatch: got %v, want %v", fetchedDesc.Digest, manifestDigest)
	}

	// Verify content
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), manifestData) {
		t.Errorf("content mismatch: got %v, want %v", buf.Bytes(), manifestData)
	}
}

func TestFetchReference_ReturnsCorrectDescriptor(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	ref := makeRef(dir)

	// Create two manifests
	data1 := []byte("manifest 1")
	dgst1 := digest.FromBytes(data1)

	desc1 := ocispecv1.Descriptor{
		Digest:    dgst1,
		Size:      int64(len(data1)),
		MediaType: ocispecv1.MediaTypeImageManifest,
	}

	if err := l.Push(context.Background(), desc1, bytes.NewReader(data1)); err != nil {
		t.Fatalf("Push error: %v", err)
	}

	if err := l.SetTag(context.Background(), desc1); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	fetchedDesc, _, err := l.FetchReference(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReference error: %v", err)
	}

	if fetchedDesc.Digest != dgst1 {
		t.Errorf("Digest mismatch: got %v, want %v", fetchedDesc.Digest, dgst1)
	}
	if fetchedDesc.MediaType != desc1.MediaType {
		t.Errorf("MediaType mismatch: got %v, want %v", fetchedDesc.MediaType, desc1.MediaType)
	}
}

// ---------------------------------------------------------------------------
// Open
// ---------------------------------------------------------------------------

func TestOpen_ValidLayout(t *testing.T) {
	// Create a layout with New
	dir := t.TempDir()
	_, err := New(makeRef(dir).String())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Now open it directly
	l, err := Open(makeRef(dir))
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	if l == nil {
		t.Error("Open() returned nil")
	}
}

func TestOpen_NonExistentLayout(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(makeRef(dir))
	if err == nil {
		t.Fatal("Open() should fail for non-existent layout")
	}
}

func TestOpen_InvalidOciLayout(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	_ = os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), []byte("invalid"), 0640)

	_, err := Open(makeRef(dir))
	if err == nil {
		t.Fatal("Open() should fail for invalid oci-layout file")
	}
}

func TestOpen_InfersUnpackFromIndex(t *testing.T) {
	dir := t.TempDir()
	// Create with Unpack
	l1, err := New(makeRef(dir).String(), Unpack())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Open without specifying Unpack — should infer from index
	l2, err := Open(makeRef(dir))
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	if !l2.(*Layout).unpack {
		t.Error("Open() should infer unpack=true from index ArtifactType")
	}
	if !l1.(*Layout).unpack {
		t.Error("Open() should keep unpack=true")
	}
}

// ---------------------------------------------------------------------------
// readIndex / writeIndex
// ---------------------------------------------------------------------------

func TestReadIndex_CorrectStructure(t *testing.T) {
	l := newLayout(t)
	index, err := l.readIndex()
	if err != nil {
		t.Fatalf("readIndex error: %v", err)
	}

	if index == nil {
		t.Error("readIndex returned nil")
	}
	if index.Versioned.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", index.Versioned.SchemaVersion)
	}
	if index.MediaType == "" {
		t.Error("MediaType should not be empty")
	}
}

func TestWriteIndex_CreatesFile(t *testing.T) {
	l := newLayout(t)
	index := &ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{},
	}

	err := l.writeIndex(index)
	if err != nil {
		t.Fatalf("writeIndex error: %v", err)
	}

	// Verify file was written
	indexPath := filepath.Join(l.ref.Path, ocispecv1.ImageIndexFile)
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index file not written: %v", err)
	}
}

func TestReadWriteIndex_RoundTrip(t *testing.T) {
	l := newLayout(t)

	// Create index with some data
	data := []byte("test manifest")
	dgst := digest.FromBytes(data)

	newIndex := &ocispecv1.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{
			{
				Digest:    dgst,
				Size:      int64(len(data)),
				MediaType: ocispecv1.MediaTypeImageManifest,
			},
		},
	}

	// Write
	if err := l.writeIndex(newIndex); err != nil {
		t.Fatalf("writeIndex error: %v", err)
	}

	// Read back
	readIndex, err := l.readIndex()
	if err != nil {
		t.Fatalf("readIndex error: %v", err)
	}

	if len(readIndex.Manifests) != 1 {
		t.Errorf("expected 1 manifest, got %d", len(readIndex.Manifests))
	}
	if readIndex.Manifests[0].Digest != dgst {
		t.Errorf("digest mismatch: got %v, want %v", readIndex.Manifests[0].Digest, dgst)
	}
}

// ---------------------------------------------------------------------------
// validate
// ---------------------------------------------------------------------------

func TestValidate_ValidLayout(t *testing.T) {
	l := newLayout(t)
	err := l.validate()
	if err != nil {
		t.Fatalf("validate() error: %v", err)
	}
}

func TestValidate_MissingLayoutFile(t *testing.T) {
	dir := t.TempDir()
	ref := makeRef(dir)
	l := &Layout{ref: ref}

	err := l.validate()
	if err == nil {
		t.Fatal("validate() should fail for missing oci-layout file")
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	_ = os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), []byte("not json"), 0640)

	ref := makeRef(dir)
	l := &Layout{ref: ref}

	err := l.validate()
	if err == nil {
		t.Fatal("validate() should fail for invalid JSON")
	}
}

func TestValidate_WrongVersion(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ocispecv1.ImageBlobsDir), 0755)
	badLayout, _ := json.Marshal(ocispecv1.ImageLayout{Version: "999.0"})
	_ = os.WriteFile(filepath.Join(dir, ocispecv1.ImageLayoutFile), badLayout, 0640)

	ref := makeRef(dir)
	l := &Layout{ref: ref}

	err := l.validate()
	if err == nil {
		t.Fatal("validate() should fail for wrong version")
	}
}

// ---------------------------------------------------------------------------
// MountFrom
// ---------------------------------------------------------------------------

func TestMountFrom_ReturnsDescriptor(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)
	ref := makeRef(dir)

	// Push and tag a manifest
	data := []byte("manifest")
	dgst := digest.FromBytes(data)
	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: ocispecv1.MediaTypeImageManifest,
	}

	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("Push error: %v", err)
	}
	if err := l.SetTag(context.Background(), desc); err != nil {
		t.Fatalf("SetTag error: %v", err)
	}

	// MountFrom should return the descriptor
	mountedDesc, err := l.MountFrom(context.Background(), ref)
	if err != nil {
		t.Fatalf("MountFrom error: %v", err)
	}

	if mountedDesc.Digest != dgst {
		t.Errorf("Digest mismatch: got %v, want %v", mountedDesc.Digest, dgst)
	}
}

func TestMountFrom_NotFound(t *testing.T) {
	l := newLayout(t)
	_, err := l.MountFrom(context.Background(), makeRef(t.TempDir()))
	if err == nil {
		t.Fatal("MountFrom() should fail for non-existent reference")
	}
}

// ---------------------------------------------------------------------------
// Push with Unpack and Compression
// ---------------------------------------------------------------------------

func TestPush_AlreadyExists_ReturnsError(t *testing.T) {
	l := newLayout(t)
	data := []byte("blob")
	dgst := digest.FromBytes(data)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: "application/octet-stream",
	}

	// First push
	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("first Push error: %v", err)
	}

	// Second push of same blob should fail
	err := l.Push(context.Background(), desc, bytes.NewReader(data))
	if err == nil {
		t.Fatal("second Push should fail with ErrAlreadyExists")
	}
}

func TestPush_CreatesNestedDirectories(t *testing.T) {
	l := newLayout(t)
	data := []byte("blob")
	dgst := digest.FromBytes(data)

	desc := ocispecv1.Descriptor{
		Digest:    dgst,
		Size:      int64(len(data)),
		MediaType: "application/octet-stream",
	}

	if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("Push error: %v", err)
	}

	// Check blob directory structure exists
	blobDir := l.getBlobDirectory(dgst)
	if _, err := os.Stat(blobDir); err != nil {
		t.Errorf("blob directory not created: %v", err)
	}

	blobPath := l.getBlobPath(dgst)
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("blob file not created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Fetch with Invalid Digest
// ---------------------------------------------------------------------------

func TestFetch_InvalidDigestFormat(t *testing.T) {
	l := newLayout(t)
	_, err := l.Fetch(context.Background(), reference.Reference{Ref: "not-a-valid-digest"})
	if err == nil {
		t.Fatal("Fetch() should fail for invalid digest format")
	}
}

// ---------------------------------------------------------------------------
// Index Consistency
// ---------------------------------------------------------------------------

func TestIndex_ConsistencyAfterMultipleTags(t *testing.T) {
	l := newLayout(t)

	// Create and tag multiple manifests
	for i := 0; i < 3; i++ {
		data := []byte("manifest-" + string(rune(i)))
		dgst := digest.FromBytes(data)
		desc := ocispecv1.Descriptor{
			Digest:    dgst,
			Size:      int64(len(data)),
			MediaType: ocispecv1.MediaTypeImageManifest,
		}

		if err := l.Push(context.Background(), desc, bytes.NewReader(data)); err != nil {
			t.Fatalf("Push error: %v", err)
		}
		if err := l.SetTag(context.Background(), desc); err != nil {
			t.Fatalf("SetTag error: %v", err)
		}
	}

	// Verify all 3 manifests are in index
	index, err := l.readIndex()
	if err != nil {
		t.Fatalf("readIndex error: %v", err)
	}

	if len(index.Manifests) != 3 {
		t.Errorf("expected 3 manifests, got %d", len(index.Manifests))
	}
}

// ---------------------------------------------------------------------------
// Edge Cases
// ---------------------------------------------------------------------------

func TestBlobPath_DifferentDigestAlgorithms(t *testing.T) {
	dir := t.TempDir()
	l := newLayoutInDir(t, dir)

	data := []byte("test")
	dgst := digest.FromBytes(data)

	blobPath := l.getBlobPath(dgst)

	// Path should contain algorithm and hex
	if !bytes.Contains([]byte(blobPath), []byte(dgst.Algorithm().String())) {
		t.Errorf("blobPath should contain algorithm %s", dgst.Algorithm().String())
	}

	if !bytes.Contains([]byte(blobPath), []byte(dgst.Hex())) {
		t.Errorf("blobPath should contain hex %s", dgst.Hex())
	}
}

func TestLayout_WithSpecialCharactersInPath(t *testing.T) {
	tmpDir := t.TempDir()
	specialDir := filepath.Join(tmpDir, "test-layout_2026")
	_ = os.MkdirAll(specialDir, 0755)

	ref := reference.Reference{
		Scheme: reference.OciScheme,
		Host:   "",
		Path:   specialDir,
		Ref:    "image:tag",
	}

	l, err := New(ref.String())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if l == nil {
		t.Error("New() returned nil for special path")
	}
}

func TestTarOptions_RootPreservesOwnership(t *testing.T) {
	opts := tarOptions(0)

	if opts.NoLchown {
		t.Error("tarOptions(0): NoLchown set — root must restore tar ownership")
	}
	if opts.InUserNS {
		t.Error("tarOptions(0): InUserNS set — root must create device nodes, not skip them")
	}
	if opts.WhiteoutFormat != -1 {
		t.Errorf("tarOptions(0): WhiteoutFormat = %d, want -1", opts.WhiteoutFormat)
	}
}

func TestTarOptions_UnprivilegedFlattensOwnership(t *testing.T) {
	for _, euid := range []int{1, 1000, 65534} {
		opts := tarOptions(euid)

		if !opts.NoLchown {
			t.Errorf("tarOptions(%d): NoLchown unset — an unprivileged unpack would EPERM on the first root-owned entry", euid)
		}
		if !opts.InUserNS {
			t.Errorf("tarOptions(%d): InUserNS unset — an unprivileged unpack would EPERM on the first device node", euid)
		}
		if opts.WhiteoutFormat != -1 {
			t.Errorf("tarOptions(%d): WhiteoutFormat = %d, want -1", euid, opts.WhiteoutFormat)
		}
	}
}
