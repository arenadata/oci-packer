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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// gateTimeout bounds how long a gated upload waits for its peers to turn up. It
// is only reached when the pack is *not* running in parallel, in which case the
// test fails on the observed peak instead of hanging.
const gateTimeout = 2 * time.Second

// packPusher keeps everything a pack uploads — the bytes, so a test can decode
// the manifest that was committed, and the overlap, so it can tell whether the
// uploads really ran at the same time. With hold set, each upload blocks until
// that many are in flight, which only ever happens if they are concurrent.
type packPusher struct {
	hold     int
	deadline time.Time
	reached  chan struct{}
	open     sync.Once

	fail map[digest.Digest]error

	mu       sync.Mutex
	inFlight int
	peak     int
	blobs    map[digest.Digest][]byte
	counts   map[digest.Digest]int
	sequence []digest.Digest
}

func newPackPusher(hold int) *packPusher {
	return &packPusher{
		hold:     hold,
		deadline: time.Now().Add(gateTimeout),
		reached:  make(chan struct{}),
		blobs:    map[digest.Digest][]byte{},
		counts:   map[digest.Digest]int{},
	}
}

func (p *packPusher) Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.inFlight++
	p.peak = max(p.peak, p.inFlight)
	inFlight := p.inFlight
	p.mu.Unlock()

	if p.hold > 0 {
		if inFlight >= p.hold {
			p.open.Do(func() { close(p.reached) })
		}
		timer := time.NewTimer(time.Until(p.deadline))
		select {
		case <-p.reached:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--

	if err = p.fail[desc.Digest]; err != nil {
		return err
	}

	p.blobs[desc.Digest] = data
	p.counts[desc.Digest]++
	p.sequence = append(p.sequence, desc.Digest)

	return nil
}

func (p *packPusher) MountFrom(context.Context, reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}

func (p *packPusher) SetTag(context.Context, ocispecv1.Descriptor) error { return nil }

func (p *packPusher) highWater() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

func (p *packPusher) count(dgst digest.Digest) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[dgst]
}

func (p *packPusher) order() []digest.Digest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.sequence)
}

// manifestAt decodes the manifest blob a descriptor points at.
func (p *packPusher) manifestAt(t *testing.T, desc ocispecv1.Descriptor) ocispecv1.Manifest {
	t.Helper()
	p.mu.Lock()
	data := p.blobs[desc.Digest]
	p.mu.Unlock()

	var m ocispecv1.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode manifest %s: %v", desc.Digest, err)
	}
	return m
}

// indexAt decodes the index blob a descriptor points at.
func (p *packPusher) indexAt(t *testing.T, desc ocispecv1.Descriptor) ocispecv1.Index {
	t.Helper()
	p.mu.Lock()
	data := p.blobs[desc.Digest]
	p.mu.Unlock()

	var i ocispecv1.Index
	if err := json.Unmarshal(data, &i); err != nil {
		t.Fatalf("decode index %s: %v", desc.Digest, err)
	}
	return i
}

// filePack builds a pack of n file items, each holding distinct content.
func filePack(t *testing.T, n int) Pack {
	t.Helper()
	dir := t.TempDir()

	items := make([]Descriptor, 0, n)
	for i := range n {
		name := fmt.Sprintf("layer-%02d.bin", i)
		writeTestFileInDir(t, dir, name, fmt.Sprintf("content of %s", name))
		items = append(items, Descriptor{From: "file://" + filepath.Join(dir, name)})
	}
	return Pack{Items: items}
}

// titles lists the titles of a manifest's layers, which is the order the pack
// file gave them in.
func titles(layers []ocispecv1.Descriptor) []string {
	names := make([]string, 0, len(layers))
	for _, l := range layers {
		names = append(names, l.Annotations[ocispecv1.AnnotationTitle])
	}
	return names
}

// TestPack_UploadsInParallel proves the blobs really do go up at the same time:
// every upload blocks until `limit` of them are in flight, which cannot happen
// unless they are running concurrently.
func TestPack_UploadsInParallel(t *testing.T) {
	const limit = 4

	pusher := newPackPusher(limit)
	if _, err := filePack(t, 8).Pack(context.Background(), pusher, WithConcurrency(limit)); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if got := pusher.highWater(); got != limit {
		t.Fatalf("peak concurrent uploads = %d, want exactly %d "+
			"(below means nothing ran in parallel, above means the limit leaks)", got, limit)
	}
}

// TestPack_HonoursConcurrencyLimit checks the slots actually cap the uploads:
// with a limit of 2 no third one may overlap, however many items there are.
func TestPack_HonoursConcurrencyLimit(t *testing.T) {
	const limit = 2

	pusher := newPackPusher(limit)
	if _, err := filePack(t, 12).Pack(context.Background(), pusher, WithConcurrency(limit)); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if got := pusher.highWater(); got != limit {
		t.Errorf("peak concurrent uploads = %d, want exactly %d", got, limit)
	}
}

// TestPack_LayerOrderMatchesPackFile is the invariant parallelism most easily
// breaks. Uploads finish in whatever order the network gives them, but a
// manifest's layers are positional — reordering them changes the artifact.
func TestPack_LayerOrderMatchesPackFile(t *testing.T) {
	const items = 10

	p := filePack(t, items)
	want := titles(nil)
	for _, item := range p.Items {
		want = append(want, filepath.Base(item.From))
	}

	// Held open until every upload is in flight, so they all finish at once and
	// nothing about the order of completion follows the order of the pack file.
	pusher := newPackPusher(items + 1)
	desc, err := p.Pack(context.Background(), pusher, WithConcurrency(items+1))
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	got := titles(pusher.manifestAt(t, desc).Layers)
	if !slices.Equal(got, want) {
		t.Errorf("layer order = %v, want the pack-file order %v", got, want)
	}
}

// TestPack_IndexManifestOrderMatchesPackFile is the same invariant one level up:
// the platforms of an index must come out in the order they were written.
func TestPack_IndexManifestOrderMatchesPackFile(t *testing.T) {
	dir := t.TempDir()
	wantPlatforms := []string{"linux/amd64", "linux/arm64", "linux/riscv64", "windows/amd64"}

	var items []Descriptor
	for i, platform := range wantPlatforms {
		name := fmt.Sprintf("bin-%d", i)
		writeTestFileInDir(t, dir, name, "content of "+name)
		items = append(items, Descriptor{From: "file://" + filepath.Join(dir, name), Platform: platform})
	}

	pusher := newPackPusher(len(items))
	desc, err := Pack{Items: items}.Pack(context.Background(), pusher, WithConcurrency(8))
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var got []string
	for _, m := range pusher.indexAt(t, desc).Manifests {
		if m.Platform == nil {
			t.Fatal("index entry has no platform")
		}
		got = append(got, m.Platform.OS+"/"+m.Platform.Architecture)
	}
	if !slices.Equal(got, wantPlatforms) {
		t.Errorf("index order = %v, want the pack-file order %v", got, wantPlatforms)
	}
}

// TestPack_SharedEmptyConfigUploadedOnce covers the blob every multi-platform
// pack pushes more than once: each manifest synthesises the same empty config,
// so without deduplication they all race to upload identical bytes.
func TestPack_SharedEmptyConfigUploadedOnce(t *testing.T) {
	dir := t.TempDir()
	var items []Descriptor
	for i, platform := range []string{"linux/amd64", "linux/arm64", "linux/riscv64"} {
		name := fmt.Sprintf("bin-%d", i)
		writeTestFileInDir(t, dir, name, "content of "+name)
		items = append(items, Descriptor{From: "file://" + filepath.Join(dir, name), Platform: platform})
	}

	pusher := newPackPusher(0)
	if _, err := (Pack{Items: items}).Pack(context.Background(), pusher, WithConcurrency(8)); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if got := pusher.count(ocispecv1.DescriptorEmptyJSON.Digest); got != 1 {
		t.Errorf("empty config uploaded %d times, want exactly 1", got)
	}
}

// TestPack_IdenticalFilesUploadedOnce covers two items that happen to hold the
// same bytes. They are separate layers with their own titles, but one blob — and
// uploading it twice at once would race on the same content in the destination.
func TestPack_IdenticalFilesUploadedOnce(t *testing.T) {
	dir := t.TempDir()
	writeTestFileInDir(t, dir, "first.bin", "identical")
	writeTestFileInDir(t, dir, "second.bin", "identical")

	p := Pack{Items: []Descriptor{
		{From: "file://" + filepath.Join(dir, "first.bin")},
		{From: "file://" + filepath.Join(dir, "second.bin")},
	}}

	pusher := newPackPusher(0)
	desc, err := p.Pack(context.Background(), pusher, WithConcurrency(4))
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	layers := pusher.manifestAt(t, desc).Layers
	if len(layers) != 2 {
		t.Fatalf("manifest has %d layers, want 2", len(layers))
	}
	if got := titles(layers); !slices.Equal(got, []string{"first.bin", "second.bin"}) {
		t.Errorf("layer titles = %v, want both files listed in order", got)
	}
	if got := pusher.count(layers[0].Digest); got != 1 {
		t.Errorf("shared blob uploaded %d times, want exactly 1", got)
	}
}

// TestPack_SequentialAtLimitOne pins what WithConcurrency(1) is meant to restore:
// one upload at a time, config before the layers, manifest last.
func TestPack_SequentialAtLimitOne(t *testing.T) {
	p := filePack(t, 3)

	pusher := newPackPusher(0) // no gate: nothing overlaps, so there is nothing to wait for
	desc, err := p.Pack(context.Background(), pusher, WithConcurrency(1))
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if got := pusher.highWater(); got != 1 {
		t.Errorf("peak concurrent uploads = %d, want 1", got)
	}

	order := pusher.order()
	if len(order) != 5 {
		t.Fatalf("uploaded %d blobs, want 5 (3 layers + config + manifest)", len(order))
	}
	if order[0] != ocispecv1.DescriptorEmptyJSON.Digest {
		t.Errorf("first upload = %s, want the config", order[0])
	}
	if order[len(order)-1] != desc.Digest {
		t.Errorf("last upload = %s, want the manifest %s", order[len(order)-1], desc.Digest)
	}
}

// TestPack_FailedUploadAbortsPack makes sure one rejected blob fails the whole
// pack and, crucially, that the manifest is not published on top of it.
func TestPack_FailedUploadAbortsPack(t *testing.T) {
	boom := errors.New("registry rejected the upload")

	p := filePack(t, 6)
	failing := digest.FromString("content of layer-03.bin")

	pusher := newPackPusher(0)
	pusher.fail = map[digest.Digest]error{failing: boom}

	desc, err := p.Pack(context.Background(), pusher, WithConcurrency(4))
	if !errors.Is(err, boom) {
		t.Fatalf("Pack error = %v, want the underlying %v", err, boom)
	}
	if desc.Digest != "" {
		t.Errorf("Pack returned descriptor %s for a failed pack", desc.Digest)
	}
}

// TestPack_AlreadyExistsIsNotReportedAsTheFailure covers a pack against a
// registry that already holds some of the blobs. "Already exists" is how the
// destination says it has the content, not a failure — filing it as one would
// make it the error shown for whatever went wrong later.
func TestPack_AlreadyExistsIsNotReportedAsTheFailure(t *testing.T) {
	boom := errors.New("registry rejected the upload")

	p := filePack(t, 6)
	pusher := newPackPusher(0)
	pusher.fail = map[digest.Digest]error{
		digest.FromString("content of layer-00.bin"): registry.ErrAlreadyExists,
		digest.FromString("content of layer-04.bin"): boom,
	}

	_, err := p.Pack(context.Background(), pusher, WithConcurrency(4))
	if !errors.Is(err, boom) {
		t.Errorf("Pack error = %v, want the real failure %v", err, boom)
	}
}

// TestPack_ClampsNonPositiveConcurrency keeps a bad -j from wedging a pack.
func TestPack_ClampsNonPositiveConcurrency(t *testing.T) {
	for _, n := range []int{0, -1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			pusher := newPackPusher(0)
			if _, err := filePack(t, 3).Pack(context.Background(), pusher, WithConcurrency(n)); err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if got := len(pusher.order()); got != 5 {
				t.Errorf("uploaded %d blobs, want 5", got)
			}
		})
	}
}

// TestPack_DownloadsSourcesInParallel covers the other half of a pack: pulling
// http:// sources in. The server holds each request until `limit` of them have
// arrived, which only happens if the downloads overlap.
func TestPack_DownloadsSourcesInParallel(t *testing.T) {
	const limit = 3

	var mu sync.Mutex
	var inFlight, peak int
	reached := make(chan struct{})
	var open sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		arrived := inFlight
		mu.Unlock()

		if arrived >= limit {
			open.Do(func() { close(reached) })
		}
		select {
		case <-reached:
		case <-time.After(gateTimeout):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()

		_, _ = w.Write([]byte("payload of " + r.URL.Path))
	}))
	defer srv.Close()

	var items []Descriptor
	for i := range 6 {
		items = append(items, Descriptor{From: fmt.Sprintf("%s/source-%d.bin", srv.URL, i)})
	}

	pack := Pack{Items: items}
	_, err := pack.Pack(context.Background(), newPackPusher(0),
		WithTmpDir(t.TempDir()), WithConcurrency(limit))
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak != limit {
		t.Errorf("peak concurrent downloads = %d, want exactly %d", peak, limit)
	}
}

// TestPack_SourcesSharingABasenameDoNotCollide covers two sources whose URLs end
// in the same file name. They used to be written to the same path under the temp
// directory, so one item silently ended up with the other's bytes — and once the
// downloads run at the same time, to the same file.
func TestPack_SourcesSharingABasenameDoNotCollide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same basename, different directories, different content.
		_, _ = w.Write([]byte("payload from " + filepath.Dir(r.URL.Path)))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	first := Descriptor{From: srv.URL + "/linux/app.tar.gz"}
	second := Descriptor{From: srv.URL + "/darwin/app.tar.gz"}

	results := make([][]Descriptor, 2)
	for i, item := range []Descriptor{first, second} {
		out, err := httpHandler(item, tmpDir)(context.Background())
		if err != nil {
			t.Fatalf("httpHandler: %v", err)
		}
		results[i] = out
	}

	paths := make([]string, 0, 2)
	for i, out := range results {
		path := strings.TrimPrefix(out[0].From, reference.FileSchema.String())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read download %d: %v", i, err)
		}
		want := []string{"payload from /linux", "payload from /darwin"}[i]
		if string(data) != want {
			t.Errorf("download %d holds %q, want %q — the two sources wrote over each other", i, data, want)
		}
		paths = append(paths, path)
	}

	if paths[0] == paths[1] {
		t.Errorf("both sources downloaded to the same path %q", paths[0])
	}
	// The name has to stay stable so a second run can reuse what is already there.
	again, err := httpHandler(first, tmpDir)(context.Background())
	if err != nil {
		t.Fatalf("httpHandler on the second run: %v", err)
	}
	if got := strings.TrimPrefix(again[0].From, reference.FileSchema.String()); got != paths[0] {
		t.Errorf("second run downloaded to %q, first run used %q: the cache cannot hit", got, paths[0])
	}
}
