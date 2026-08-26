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
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arenadata/oci-packer/internal/parallel"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ---------------------------------------------------------------------------
// extendAnnotations
// ---------------------------------------------------------------------------

func TestExtendAnnotations_AddsCreatedTimestamp(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Second)
	result := extendAnnotations(nil)
	after := time.Now().UTC().Truncate(time.Second)

	created, ok := result[ocispecv1.AnnotationCreated]
	if !ok {
		t.Fatal("AnnotationCreated key not set")
	}
	ts, err := time.Parse(time.RFC3339, created)
	if err != nil {
		t.Fatalf("AnnotationCreated is not RFC3339: %q — %v", created, err)
	}
	ts = ts.UTC()
	if ts.Before(before) || ts.After(after.Add(time.Second)) {
		t.Errorf("timestamp %v not in expected range [%v, %v]", ts, before, after)
	}
}

func TestExtendAnnotations_PreservesExistingKeys(t *testing.T) {
	input := map[string]string{"org.example.version": "1.0", "custom": "value"}
	result := extendAnnotations(input)

	if result["org.example.version"] != "1.0" {
		t.Errorf("existing key lost: %v", result)
	}
	if result["custom"] != "value" {
		t.Errorf("existing key lost: %v", result)
	}
}

func TestExtendAnnotations_DoesNotMutateInput(t *testing.T) {
	input := map[string]string{"key": "original"}
	extendAnnotations(input)

	if _, hasCreated := input[ocispecv1.AnnotationCreated]; hasCreated {
		t.Error("extendAnnotations mutated the input map")
	}
}

func TestExtendAnnotations_NilInput(t *testing.T) {
	result := extendAnnotations(nil)
	if len(result) == 0 {
		t.Error("result should contain at least the created annotation")
	}
}

// ---------------------------------------------------------------------------
// handleItem
// ---------------------------------------------------------------------------

func TestHandleItem_FileHandler(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "contents")
	item := Descriptor{From: "file://" + f}

	result, err := handleItem(context.Background(), item, builderOptions{}, parallel.NewBudget(DefaultConcurrency))
	if err != nil {
		t.Fatalf("handleItem() error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(result))
	}
}

func TestHandleItem_DirHandler(t *testing.T) {
	dir := t.TempDir()
	writeTestFileInDir(t, dir, "a.bin", "aaa")
	writeTestFileInDir(t, dir, "b.bin", "bbb")

	item := Descriptor{From: "dir://" + dir}
	result, err := handleItem(context.Background(), item, builderOptions{}, parallel.NewBudget(DefaultConcurrency))
	if err != nil {
		t.Fatalf("handleItem() error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 descriptors, got %d", len(result))
	}
}

func TestHandleItem_UnsupportedSource(t *testing.T) {
	// OCI sources (oci://, cr://) are not handled by handleItem — they return an error
	item := Descriptor{From: "oci://registry/image:latest"}
	_, err := handleItem(context.Background(), item, builderOptions{}, parallel.NewBudget(DefaultConcurrency))
	if err == nil {
		t.Fatal("expected error for OCI source in handleItem")
	}
	if !strings.Contains(err.Error(), "unsupported source type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleItem_MissingFile(t *testing.T) {
	item := Descriptor{From: "file:///nonexistent/file.bin"}
	_, err := handleItem(context.Background(), item, builderOptions{}, parallel.NewBudget(DefaultConcurrency))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestHandleItem_MissingDir(t *testing.T) {
	item := Descriptor{From: "dir:///nonexistent/dir"}
	_, err := handleItem(context.Background(), item, builderOptions{}, parallel.NewBudget(DefaultConcurrency))
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

// ---------------------------------------------------------------------------
// WithTmpDir build option
// ---------------------------------------------------------------------------

func TestWithTmpDir(t *testing.T) {
	customDir := t.TempDir()
	var opts builderOptions
	WithTmpDir(customDir)(&opts)
	if opts.tmpDir != customDir {
		t.Errorf("tmpDir = %q, want %q", opts.tmpDir, customDir)
	}
}

// ---------------------------------------------------------------------------
// Pack.Pack — integration tests with a mock pusher
// ---------------------------------------------------------------------------

func TestPackPack_FailsValidation(t *testing.T) {
	p := Pack{Items: []Descriptor{}} // empty items — fails validation
	_, err := p.Pack(context.Background(), &mockPusher{})
	if err == nil {
		t.Fatal("Pack() should fail when validation fails")
	}
}

func TestPackPack_ConfigWithOCIItemConflict(t *testing.T) {
	p := Pack{
		Metadata: Metadata{
			Config: &ConfigDescriptor{From: "file://cfg.json"},
		},
		Items: []Descriptor{
			{From: "oci://registry/image:latest"},
		},
	}
	_, err := p.Pack(context.Background(), &mockPusher{})
	if err == nil {
		t.Fatal("Pack() should fail when metadata.config is used with oci items")
	}
	if !strings.Contains(err.Error(), "metadata.config") && !strings.Contains(err.Error(), "oci") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPackPack_ConfigWithPlatformItemConflict(t *testing.T) {
	p := Pack{
		Metadata: Metadata{
			Config: &ConfigDescriptor{From: "file://cfg.json"},
		},
		Items: []Descriptor{
			{From: "file://data.bin", Platform: "linux/amd64"},
		},
	}
	_, err := p.Pack(context.Background(), &mockPusher{})
	if err == nil {
		t.Fatal("Pack() should fail when metadata.config is used with platform items")
	}
}

func TestPackPack_ManifestWithSingleFile(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "test content")

	p := Pack{
		Items: []Descriptor{
			{From: "file://" + f, Type: "application/octet-stream"},
		},
	}
	pusher := &mockPusher{}
	_, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	// A single-file manifest requires at minimum:
	//   1 push — the blob layer
	//   1 push — the synthesised config blob
	//   1 push — the image manifest itself
	const minExpectedPushes = 3
	if pusher.count() < minExpectedPushes {
		t.Errorf("expected at least %d push calls (blob+config+manifest), got %d",
			minExpectedPushes, pusher.count())
	}
}

func TestPackPack_ManifestWithMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.bin")
	f2 := filepath.Join(dir, "b.bin")
	_ = os.WriteFile(f1, []byte("aaa"), 0600)
	_ = os.WriteFile(f2, []byte("bbb"), 0600)

	p := Pack{
		Items: []Descriptor{
			{From: "file://" + f1},
			{From: "file://" + f2},
		},
	}
	pusher := &mockPusher{}
	_, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
}

func TestPackPack_IndexCreatedForPlatformItem(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "data")

	p := Pack{
		Items: []Descriptor{
			{From: "file://" + f, Platform: "linux/amd64"},
		},
	}
	pusher := &mockPusher{}
	desc, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	// Index manifest has MediaType ImageIndex
	if desc.MediaType != ocispecv1.MediaTypeImageIndex {
		t.Errorf("expected ImageIndex MediaType for platform item, got %q", desc.MediaType)
	}
}

// A cr:// item of an index is the member itself: the index carries the mounted
// manifest's descriptor — its own artifactType, the item's annotations on top —
// and no manifest is built around it. A config on such an item is refused.
func TestPackPack_IndexOCIItemIsTheMemberItself(t *testing.T) {
	f := writeTestFile(t, "schema.json", "{}")
	p := Pack{
		Type: "application/vnd.example.pack",
		Items: []Descriptor{
			{From: "cr://registry.example/images/app:1.0", Platform: "linux/amd64",
				Annotations: map[string]string{"io.example.key": "app"}},
			{From: "file://" + f, Type: "application/vnd.example.schema"},
		},
	}
	pusher := &mockPusher{}
	desc, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	if desc.MediaType != ocispecv1.MediaTypeImageIndex {
		t.Fatalf("expected an index, got %q", desc.MediaType)
	}
	if len(pusher.mounted) != 1 || pusher.mounted[0].Path != "images/app" {
		t.Fatalf("expected one mount of images/app, got %+v", pusher.mounted)
	}
	// Exactly one manifest is built — the schema item's; the image is not wrapped.
	manifests := 0
	for _, d := range pusher.pushes() {
		if d.MediaType == ocispecv1.MediaTypeImageManifest {
			manifests++
		}
	}
	if manifests != 1 {
		t.Fatalf("expected one built manifest (the schema item), got %d", manifests)
	}

	// The member keeps the source's artifactType unless the item names one,
	// and takes the item's annotations.
	m := manifest{Descriptors: []Descriptor{p.Items[0]}, budget: parallel.NewBudget(1)}
	member, err := m.Push(context.Background(), pusher)
	if err != nil {
		t.Fatal(err)
	}
	if member.ArtifactType != "application/vnd.example.source" || member.Annotations["io.example.key"] != "app" {
		t.Fatalf("unexpected member: %+v", member)
	}
	m.Descriptors[0].Type = "application/vnd.example.override"
	if member, _ = m.Push(context.Background(), pusher); member.ArtifactType != "application/vnd.example.override" {
		t.Fatalf("the item's type must win: %+v", member)
	}

	withConfig := Pack{Items: []Descriptor{
		{From: "cr://registry.example/images/app:1.0", Config: &ConfigDescriptor{From: "file://" + f}},
	}}
	if _, err := withConfig.Pack(context.Background(), &mockPusher{}); err == nil {
		t.Fatal("a mounted item carrying a config must be refused")
	}
}

func TestPackPack_ManifestCreatedForPlainItems(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "data")

	p := Pack{
		Items: []Descriptor{
			{From: "file://" + f},
		},
	}
	pusher := &mockPusher{}
	desc, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	if desc.MediaType != ocispecv1.MediaTypeImageManifest {
		t.Errorf("expected ImageManifest MediaType, got %q", desc.MediaType)
	}
}

func TestPackPack_AlreadyExistsIsIgnored(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "data")

	p := Pack{
		Items: []Descriptor{
			{From: "file://" + f},
		},
	}
	// A pusher that always returns AlreadyExists should not cause an error
	pusher := &mockPusher{alwaysAlreadyExists: true}
	_, err := p.Pack(context.Background(), pusher)
	if err != nil {
		t.Fatalf("Pack() should tolerate AlreadyExists errors, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mock helpers
// ---------------------------------------------------------------------------

// mockPusher counts pushes. Packing uploads blobs concurrently, so it is called
// from several goroutines at once.
type mockPusher struct {
	alwaysAlreadyExists bool

	mu        sync.Mutex
	pushCount int
	pushed    []ocispecv1.Descriptor
	mounted   []reference.Reference
}

// MountFrom answers the way a registry does: with the descriptor of what was
// resolved and mounted, carrying its own artifactType.
func (m *mockPusher) MountFrom(_ context.Context, ref reference.Reference) (ocispecv1.Descriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mounted = append(m.mounted, ref)
	return ocispecv1.Descriptor{
		MediaType:    ocispecv1.MediaTypeImageManifest,
		ArtifactType: "application/vnd.example.source",
		Digest:       digest.FromString("mounted:" + ref.Path + ":" + ref.Ref),
		Size:         42,
	}, nil
}

func (m *mockPusher) Push(_ context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushCount++
	m.pushed = append(m.pushed, desc)

	if m.alwaysAlreadyExists {
		return registry.ErrAlreadyExists
	}
	return nil
}

// pushes returns the descriptors received so far, in completion order.
func (m *mockPusher) pushes() []ocispecv1.Descriptor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.pushed)
}

func (m *mockPusher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pushCount
}

func (m *mockPusher) SetTag(context.Context, ocispecv1.Descriptor) error {
	return nil
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestPackPack_ContextCancelled(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "data")
	p := Pack{Items: []Descriptor{{From: "file://" + f}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Pack is called

	// Must not panic regardless of whether Pack honours ctx.
	_, _ = p.Pack(ctx, &mockPusher{})
}

func TestPackPack_ContextCancelledDuringPush(t *testing.T) {
	f := writeTestFile(t, "blob.bin", "data")
	p := Pack{Items: []Descriptor{{From: "file://" + f}}}

	ctx, cancel := context.WithCancel(context.Background())
	pusher := &mockCancelPusher{cancel: cancel}

	// Must not panic or deadlock.
	_, _ = p.Pack(ctx, pusher)
}

// mockCancelPusher cancels the context on its first Push call. Uploads run
// concurrently, so "first" has to be decided under a lock.
type mockCancelPusher struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (m *mockCancelPusher) MountFrom(_ context.Context, _ reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}
func (m *mockCancelPusher) Push(ctx context.Context, _ ocispecv1.Descriptor, _ io.Reader) error {
	m.once.Do(m.cancel)
	return ctx.Err()
}
func (m *mockCancelPusher) SetTag(_ context.Context, _ ocispecv1.Descriptor) error { return nil }

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func writeTestFileInDir(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
