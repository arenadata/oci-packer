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

package packer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// fileHandler
// ---------------------------------------------------------------------------

func TestFileHandler_ReturnsDescriptorForExistingFile(t *testing.T) {
	path := writeTestFile(t, "data.json", `{"key":"value"}`)
	desc := Descriptor{From: "file://" + path}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("fileHandler() error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(result))
	}

	got := result[0]
	if got.From != desc.From {
		t.Errorf("From = %q, want %q", got.From, desc.From)
	}
}

func TestFileHandler_SetsAnnotationTitle(t *testing.T) {
	path := writeTestFile(t, "myfile.tar", "content")
	desc := Descriptor{From: "file://" + path}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("fileHandler() error: %v", err)
	}

	title := result[0].Annotations[ocispecv1.AnnotationTitle]
	if title != "myfile.tar" {
		t.Errorf("AnnotationTitle = %q, want %q", title, "myfile.tar")
	}
}

func TestFileHandler_PreservesExistingAnnotations(t *testing.T) {
	path := writeTestFile(t, "file.bin", "data")
	desc := Descriptor{
		From:        "file://" + path,
		Annotations: map[string]string{"custom": "annotation"},
	}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("fileHandler() error: %v", err)
	}
	if result[0].Annotations["custom"] != "annotation" {
		t.Errorf("custom annotation lost: %v", result[0].Annotations)
	}
}

func TestFileHandler_InfersMediaTypeFromExtension(t *testing.T) {
	path := writeTestFile(t, "layer.tgz", "gzip content")
	desc := Descriptor{From: "file://" + path}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].Type != ocispecv1.MediaTypeImageLayerGzip {
		t.Errorf("Type = %q, want %q", result[0].Type, ocispecv1.MediaTypeImageLayerGzip)
	}
}

func TestFileHandler_ExplicitTypeOverridesInferred(t *testing.T) {
	path := writeTestFile(t, "layer.tgz", "content")
	desc := Descriptor{From: "file://" + path, Type: "application/custom"}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].Type != "application/custom" {
		t.Errorf("Type = %q, want application/custom", result[0].Type)
	}
}

func TestFileHandler_ErrorForMissingFile(t *testing.T) {
	desc := Descriptor{From: "file:///no/such/file.bin"}
	_, err := fileHandler(desc)(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFileHandler_ErrorWhenPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	desc := Descriptor{From: "file://" + dir}
	_, err := fileHandler(desc)(context.Background())
	if err == nil {
		t.Fatal("expected error when path is a directory")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFileHandler_PreservesPlatform(t *testing.T) {
	path := writeTestFile(t, "blob.bin", "data")
	desc := Descriptor{From: "file://" + path, Platform: "linux/arm64"}

	result, err := fileHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].Platform != "linux/arm64" {
		t.Errorf("Platform = %q, want linux/arm64", result[0].Platform)
	}
}

// ---------------------------------------------------------------------------
// walkDirHandler
// ---------------------------------------------------------------------------

func TestWalkDirHandler_ReturnsOneDescriptorPerFile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), []byte("a"))
	mustWriteFile(t, filepath.Join(dir, "b.txt"), []byte("b"))

	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("walkDirHandler() error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 descriptors, got %d", len(result))
	}
}

func TestWalkDirHandler_AnnotationTitleIsRelativePath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "myfile.txt"), []byte("data"))

	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("walkDirHandler() error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	title := result[0].Annotations[ocispecv1.AnnotationTitle]
	if title != "myfile.txt" {
		t.Errorf("AnnotationTitle = %q, want relative path %q", title, "myfile.txt")
	}
}

func TestWalkDirHandler_AnnotationTitleIsRelativePath_Nested(t *testing.T) {
	// Verify that for a file inside a subdirectory the title contains the
	// full relative path (subdir/file.txt), not just the bare filename.
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustWriteFile(t, filepath.Join(sub, "nested.txt"), []byte("n"))

	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("walkDirHandler() error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(result))
	}
	want := filepath.Join("subdir", "nested.txt")
	title := result[0].Annotations[ocispecv1.AnnotationTitle]
	if title != want {
		t.Errorf("AnnotationTitle = %q, want %q", title, want)
	}
}

func TestWalkDirHandler_DescriptorFromFieldHasFileSchema(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "item.bin"), []byte("x"))

	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.HasPrefix(result[0].From, reference.FileSchema.String()) {
		t.Errorf("From = %q, should start with %q", result[0].From, reference.FileSchema.String())
	}
}

func TestWalkDirHandler_EmptyDirReturnsNoDescriptors(t *testing.T) {
	dir := t.TempDir()
	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 descriptors for empty dir, got %d", len(result))
	}
}

func TestWalkDirHandler_NestedFilesIncluded(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "root.txt"), []byte("root"))
	mustWriteFile(t, filepath.Join(sub, "nested.txt"), []byte("nested"))

	desc := Descriptor{From: "dir://" + dir}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 descriptors (root + nested), got %d", len(result))
	}
}

func TestWalkDirHandler_MissingDirReturnsError(t *testing.T) {
	desc := Descriptor{From: "dir:///nonexistent/path"}
	_, err := walkDirHandler(desc)(context.Background())
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestWalkDirHandler_FilePathReturnsError(t *testing.T) {
	f := writeTestFile(t, "notadir.bin", "data")
	desc := Descriptor{From: "dir://" + f}
	_, err := walkDirHandler(desc)(context.Background())
	if err == nil {
		t.Fatal("expected error when path is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWalkDirHandler_PreservesPlatform(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "blob.bin"), []byte("data"))

	desc := Descriptor{From: "dir://" + dir, Platform: "linux/amd64"}
	result, err := walkDirHandler(desc)(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	for _, d := range result {
		if d.Platform != "linux/amd64" {
			t.Errorf("Platform = %q, want linux/amd64", d.Platform)
		}
	}
}

// ---------------------------------------------------------------------------
// httpHandler
// ---------------------------------------------------------------------------

func TestHttpHandler_SuccessfulDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("binary content"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	desc := Descriptor{From: srv.URL + "/myfile.bin"}

	result, err := httpHandler(desc, tmpDir)(context.Background())
	if err != nil {
		t.Fatalf("httpHandler() error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(result))
	}
	if !strings.HasPrefix(result[0].From, reference.FileSchema.String()) {
		t.Errorf("result From = %q, should be file:// path", result[0].From)
	}
}

func TestHttpHandler_Non200StatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	desc := Descriptor{From: srv.URL + "/missing.bin"}
	_, err := httpHandler(desc, t.TempDir())(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestHttpHandler_DownloadedFileStoredInTmpDir(t *testing.T) {
	content := []byte("downloaded bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	desc := Descriptor{From: srv.URL + "/artifact.tar.gz"}
	result, err := httpHandler(desc, tmpDir)(context.Background())
	if err != nil {
		t.Fatalf("httpHandler() error: %v", err)
	}

	// Verify the file was placed in tmpDir
	filePath := strings.TrimPrefix(result[0].From, reference.FileSchema.String())
	if !strings.HasPrefix(filePath, tmpDir) {
		t.Errorf("file path %q not under tmpDir %q", filePath, tmpDir)
	}

	// Verify file content
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("file content = %q, want %q", got, content)
	}
}

func TestHttpHandler_PreservesDescriptorType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	desc := Descriptor{From: srv.URL + "/f.bin", Type: "application/custom"}
	result, err := httpHandler(desc, t.TempDir())(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].Type != "application/custom" {
		t.Errorf("Type = %q, want application/custom", result[0].Type)
	}
}

func TestHttpHandler_PreservesAnnotations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	desc := Descriptor{
		From:        srv.URL + "/f.bin",
		Annotations: map[string]string{"org.example.key": "val"},
	}
	result, err := httpHandler(desc, t.TempDir())(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result[0].Annotations["org.example.key"] != "val" {
		t.Errorf("annotation lost: %v", result[0].Annotations)
	}
}

func TestHttpHandler_InvalidURL(t *testing.T) {
	// Start a real server then immediately close it so the port is unreachable
	// but well-defined (avoids OS-specific behaviour around port 0).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	desc := Descriptor{From: addr + "/nope"}
	_, err := httpHandler(desc, t.TempDir())(context.Background())
	if err == nil {
		t.Fatal("expected connection error for closed server")
	}
}

// mustWriteFile writes data to path and calls t.Fatal on error.
// Use this in test setup instead of ignoring os.WriteFile errors.
func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("mustWriteFile(%q): %v", path, err)
	}
}
