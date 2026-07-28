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

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// gateTimeout bounds how long a gated push waits for its peers to show up. It is
// only ever hit when the copy is *not* running in parallel, in which case the
// test fails on the observed peak rather than hanging.
const gateTimeout = 2 * time.Second

// blobStore is an in-memory content-addressed Fetcher used to stand in for a
// source registry or layout.
type blobStore struct {
	mockFetcher
}

func newBlobStore() *blobStore {
	return &blobStore{mockFetcher{blobs: map[string][]byte{}}}
}

func (s *blobStore) add(mediaType string, data []byte) ocispecv1.Descriptor {
	s.blobs[digest.FromBytes(data).String()] = data
	return ocispecv1.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
}

func (s *blobStore) addJSON(t *testing.T, mediaType string, v any) ocispecv1.Descriptor {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", mediaType, err)
	}
	return s.add(mediaType, data)
}

// addLayers stores n distinct layer blobs tagged with the given name prefix.
func (s *blobStore) addLayers(name string, n int) []ocispecv1.Descriptor {
	layers := make([]ocispecv1.Descriptor, 0, n)
	for i := range n {
		layers = append(layers, s.add(ocispecv1.MediaTypeImageLayerGzip, fmt.Appendf(nil, "%s-layer-%d", name, i)))
	}
	return layers
}

// addManifest stores a config blob plus a manifest listing the given layers.
func (s *blobStore) addManifest(t *testing.T, name string, layers []ocispecv1.Descriptor) ocispecv1.Descriptor {
	t.Helper()
	config := s.add(ocispecv1.MediaTypeImageConfig, fmt.Appendf(nil, "%s-config", name))
	return s.addJSON(t, ocispecv1.MediaTypeImageManifest, ocispecv1.Manifest{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Config:    config,
		Layers:    layers,
	})
}

// gatedPusher records everything pushed to it and how many pushes overlap. When
// hold is set, each push blocks until that many pushes are in flight at once, so
// a test observes real overlap instead of inferring it from timings.
type gatedPusher struct {
	hold     int
	deadline time.Time
	reached  chan struct{}
	open     sync.Once

	failOn digest.Digest
	err    error

	mu       sync.Mutex
	inFlight int
	peak     int
	pushed   []digest.Digest
}

func newGatedPusher(hold int) *gatedPusher {
	return &gatedPusher{hold: hold, deadline: time.Now().Add(gateTimeout), reached: make(chan struct{})}
}

func (p *gatedPusher) Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
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

	if p.err != nil && desc.Digest == p.failOn {
		return p.err
	}
	p.pushed = append(p.pushed, desc.Digest)

	return nil
}

func (p *gatedPusher) MountFrom(context.Context, reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}

func (p *gatedPusher) SetTag(context.Context, ocispecv1.Descriptor) error { return nil }

// order returns the digests in the order their pushes completed.
func (p *gatedPusher) order() []digest.Digest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.pushed)
}

func (p *gatedPusher) count(dgst digest.Digest) int {
	n := 0
	for _, d := range p.order() {
		if d == dgst {
			n++
		}
	}
	return n
}

func (p *gatedPusher) highWater() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// TestCopy_TransfersLayersInParallel proves the layers of one manifest really do
// move at the same time: every push blocks until `limit` of them are in flight,
// which only ever happens if the copier runs them concurrently.
func TestCopy_TransfersLayersInParallel(t *testing.T) {
	const limit = 4

	src := newBlobStore()
	manifest := src.addManifest(t, "app", src.addLayers("app", 8))
	dst := newGatedPusher(limit)

	if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(limit)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.highWater(); got != limit {
		t.Fatalf("peak concurrent pushes = %d, want exactly %d "+
			"(below means no parallelism, above means the limit leaks)", got, limit)
	}
	if got := len(dst.order()); got != 10 {
		t.Errorf("pushed %d blobs, want 10 (8 layers + config + manifest)", got)
	}
}

// TestCopy_HonoursConcurrencyLimit checks the semaphore actually caps transfers:
// with a limit of 2 no third push may overlap, however many layers there are.
func TestCopy_HonoursConcurrencyLimit(t *testing.T) {
	const limit = 2

	src := newBlobStore()
	manifest := src.addManifest(t, "app", src.addLayers("app", 12))
	dst := newGatedPusher(limit)

	if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(limit)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.highWater(); got != limit {
		t.Errorf("peak concurrent pushes = %d, want exactly %d", got, limit)
	}
}

// countingFetcher records how much of the copy hits the source: how many reads
// there are in total, how many overlap, and how many goroutines are alive while
// they do.
type countingFetcher struct {
	*blobStore
	delay time.Duration // held open this long, so overlapping reads are visible

	mu         sync.Mutex
	inFlight   int
	peak       int
	total      int
	peakThread int
}

func (f *countingFetcher) Fetch(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
	f.mu.Lock()
	f.inFlight++
	f.total++
	f.peak = max(f.peak, f.inFlight)
	f.peakThread = max(f.peakThread, runtime.NumGoroutine())
	f.mu.Unlock()

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()

	return f.blobStore.Fetch(ctx, ref)
}

func (f *countingFetcher) stats() (peak, total, goroutines int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak, f.total, f.peakThread
}

func (f *countingFetcher) highWater() int {
	peak, _, _ := f.stats()
	return peak
}

// TestCopy_SharedSubtreeCopiedOnce guards the difference between walking routes
// and walking digests. Nesting indexes that each list the level below twice
// doubles the number of routes to the bottom per level while adding one blob, so
// a copier that follows routes does exponential work — and, running them
// concurrently, holds an exponential number of goroutines open at once.
func TestCopy_SharedSubtreeCopiedOnce(t *testing.T) {
	const depth = 14

	src := newBlobStore()
	node := src.addManifest(t, "leaf", src.addLayers("leaf", 1))
	for range depth {
		node = src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
			MediaType: ocispecv1.MediaTypeImageIndex,
			Manifests: []ocispecv1.Descriptor{node, node},
		})
	}

	// depth indexes, plus the leaf manifest, its config and its layer.
	const blobs = depth + 3

	counting := &countingFetcher{blobStore: src}
	if err := Copy(context.Background(), newGatedPusher(0), counting, node, WithConcurrency(4)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	// Two reads per blob: one to walk it, one to transfer it.
	_, total, goroutines := counting.stats()
	if want := 2 * blobs; total > want {
		t.Errorf("%d source reads for %d distinct blobs, want at most %d: the shared subtree is being copied once per route to it",
			total, blobs, want)
	}
	if goroutines > 100 {
		t.Errorf("%d goroutines alive during the copy, want a handful: routes, not digests, are driving the fan-out", goroutines)
	}
}

// TestCopy_LimitsSourceRequests covers an index far wider than the limit. The
// manifests are fetched to walk them, not to write them, so a cap that only
// guarded writes would let all twenty of those reads hit the registry at once.
func TestCopy_LimitsSourceRequests(t *testing.T) {
	const limit = 3

	src := newBlobStore()
	manifests := make([]ocispecv1.Descriptor, 0, 20)
	for i := range 20 {
		name := fmt.Sprintf("m%d", i)
		manifests = append(manifests, src.addManifest(t, name, src.addLayers(name, 2)))
	}
	index := src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: manifests,
	})

	counting := &countingFetcher{blobStore: src, delay: time.Millisecond}
	if err := Copy(context.Background(), newGatedPusher(0), counting, index, WithConcurrency(limit)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := counting.highWater(); got > limit {
		t.Errorf("peak concurrent source reads = %d, want at most %d", got, limit)
	}
}

// TestCopy_SequentialByDefaultAtLimitOne pins the pre-parallel behaviour that
// WithConcurrency(1) is meant to restore: one transfer at a time, children in
// manifest order, parent last.
func TestCopy_SequentialByDefaultAtLimitOne(t *testing.T) {
	src := newBlobStore()
	layers := src.addLayers("app", 3)
	manifest := src.addManifest(t, "app", layers)
	dst := newGatedPusher(0) // no gate: nothing to wait for when nothing overlaps

	if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(1)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.highWater(); got != 1 {
		t.Errorf("peak concurrent pushes = %d, want 1", got)
	}

	got := dst.order()
	if len(got) != 5 {
		t.Fatalf("pushed %d blobs, want 5 (3 layers + config + manifest)", len(got))
	}
	// config first, then the layers in manifest order, then the manifest itself.
	for i, layer := range layers {
		if got[i+1] != layer.Digest {
			t.Errorf("push[%d] = %s, want layer %s", i+1, got[i+1], layer.Digest)
		}
	}
	if got[len(got)-1] != manifest.Digest {
		t.Errorf("last push = %s, want the manifest %s", got[len(got)-1], manifest.Digest)
	}
}

// TestCopy_ParentPushedAfterChildren guards the invariant parallelism could
// easily break: a manifest or index must never land before the blobs it points
// at, or the destination briefly references content that is not there.
func TestCopy_ParentPushedAfterChildren(t *testing.T) {
	src := newBlobStore()
	amd64 := src.addManifest(t, "amd64", src.addLayers("amd64", 4))
	arm64 := src.addManifest(t, "arm64", src.addLayers("arm64", 4))
	index := src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{amd64, arm64},
	})
	dst := newGatedPusher(0)

	if err := Copy(context.Background(), dst, src, index, WithConcurrency(4)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	got := dst.order()
	if len(got) != 13 {
		t.Fatalf("pushed %d blobs, want 13 (2x(4 layers + config + manifest) + index)", len(got))
	}
	if got[len(got)-1] != index.Digest {
		t.Errorf("index pushed at position %d, want last", slices.Index(got, index.Digest))
	}

	// Each manifest must follow its own config and layers.
	for _, manifest := range []ocispecv1.Descriptor{amd64, arm64} {
		var parsed ocispecv1.Manifest
		if err := json.Unmarshal(src.blobs[manifest.Digest.String()], &parsed); err != nil {
			t.Fatal(err)
		}
		at := slices.Index(got, manifest.Digest)
		if at < 0 {
			t.Fatalf("manifest %s never pushed", manifest.Digest)
		}
		for _, child := range manifestChildren(parsed) {
			if childAt := slices.Index(got, child.Digest); childAt < 0 || childAt > at {
				t.Errorf("child %s pushed at %d, after its manifest at %d", child.Digest, childAt, at)
			}
		}
	}
}

// TestCopy_SharedLayerTransferredOnce covers the case parallelism makes unsafe:
// two manifests of one index listing the same layer. Copied concurrently without
// deduplication, both would stream into the same blob path at the same time.
func TestCopy_SharedLayerTransferredOnce(t *testing.T) {
	src := newBlobStore()
	shared := src.addLayers("shared", 1)[0]
	amd64 := src.addManifest(t, "amd64", append(src.addLayers("amd64", 3), shared))
	arm64 := src.addManifest(t, "arm64", append(src.addLayers("arm64", 3), shared))
	index := src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{amd64, arm64},
	})
	dst := newGatedPusher(0)

	if err := Copy(context.Background(), dst, src, index, WithConcurrency(8)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.count(shared.Digest); got != 1 {
		t.Errorf("shared layer pushed %d times, want exactly 1", got)
	}
	if got := len(dst.order()); got != 12 {
		t.Errorf("pushed %d blobs, want 12 (7 distinct layers + 2 configs + 2 manifests + index)", got)
	}
}

// TestCopy_RepeatedLayerInOneManifest exercises a manifest that lists the same
// digest twice — legal for the shared empty layer — where both references are
// siblings racing each other rather than living under different parents.
func TestCopy_RepeatedLayerInOneManifest(t *testing.T) {
	src := newBlobStore()
	empty := src.add(ocispecv1.MediaTypeImageLayerGzip, []byte("empty"))
	manifest := src.addManifest(t, "app", []ocispecv1.Descriptor{empty, empty, empty})
	dst := newGatedPusher(0)

	if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(4)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.count(empty.Digest); got != 1 {
		t.Errorf("repeated layer pushed %d times, want exactly 1", got)
	}
}

// TestCopy_FailedLayerAbortsCopy makes sure one bad layer fails the whole copy
// and, crucially, that the manifest is not published on top of it.
func TestCopy_FailedLayerAbortsCopy(t *testing.T) {
	boom := errors.New("layer upload rejected")

	src := newBlobStore()
	layers := src.addLayers("app", 6)
	manifest := src.addManifest(t, "app", layers)

	dst := newGatedPusher(0)
	dst.failOn, dst.err = layers[3].Digest, boom

	err := Copy(context.Background(), dst, src, manifest, WithConcurrency(4))
	if !errors.Is(err, boom) {
		t.Fatalf("Copy error = %v, want %v", err, boom)
	}
	if slices.Contains(dst.order(), manifest.Digest) {
		t.Error("manifest was pushed even though one of its layers failed")
	}
}

// scriptedPusher and holdingFetcher below drive one exact interleaving of a
// multi-platform copy, the one that decides which error the user is shown.
//
// A failing layer cancels its own manifest's transfers, and a layer shared with
// the other manifest is one of them — so the other manifest's walker adopts a
// context.Canceled through a different errgroup than the one that saw the real
// error. Meanwhile a push that ignores cancellation holds the failing manifest
// open, so the meaningless error is the one that unwinds first.
type scriptedPusher struct {
	shared, failing, slow digest.Digest
	err                   error

	started chan struct{} // closed once the shared layer's push is under way
	once    sync.Once

	mu     sync.Mutex
	pushed []digest.Digest
}

func (p *scriptedPusher) Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}

	switch desc.Digest {
	case p.shared:
		// Claimed here, by the manifest that is about to fail.
		p.once.Do(func() { close(p.started) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gateTimeout):
			return errors.New("shared layer was never cancelled")
		}

	case p.failing:
		<-p.started
		return p.err

	case p.slow:
		<-p.started
		// Deaf to cancellation, so this manifest unwinds last.
		time.Sleep(50 * time.Millisecond)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushed = append(p.pushed, desc.Digest)

	return nil
}

func (p *scriptedPusher) MountFrom(context.Context, reference.Reference) (ocispecv1.Descriptor, error) {
	return ocispecv1.Descriptor{}, nil
}

func (p *scriptedPusher) SetTag(context.Context, ocispecv1.Descriptor) error { return nil }

// holdingFetcher keeps one descriptor unreadable until a signal fires, so the
// second manifest cannot start walking before the first one owns the shared
// layer.
type holdingFetcher struct {
	*blobStore
	hold  digest.Digest
	until <-chan struct{}
}

func (f holdingFetcher) Fetch(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
	if ref.Ref == f.hold.String() {
		select {
		case <-f.until:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(gateTimeout):
			return nil, errors.New("held descriptor was never released")
		}
	}
	return f.blobStore.Fetch(ctx, ref)
}

// TestCopy_ReportsTheRealFailureNotTheFallout pins the diagnostic that matters
// when copying a multi-platform image fails: "registry rejected the upload" and
// not "context canceled", whichever subtree unwinds first.
func TestCopy_ReportsTheRealFailureNotTheFallout(t *testing.T) {
	boom := errors.New("registry rejected the upload: 507 insufficient storage")

	src := newBlobStore()
	shared := src.add(ocispecv1.MediaTypeImageLayerGzip, []byte("shared-layer"))
	failing := src.add(ocispecv1.MediaTypeImageLayerGzip, []byte("failing-layer"))
	slow := src.add(ocispecv1.MediaTypeImageLayerGzip, []byte("slow-layer"))

	amd64 := src.addManifest(t, "amd64", []ocispecv1.Descriptor{failing, shared, slow})
	arm64 := src.addManifest(t, "arm64", []ocispecv1.Descriptor{shared})
	index := src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{amd64, arm64},
	})

	dst := &scriptedPusher{
		shared:  shared.Digest,
		failing: failing.Digest,
		slow:    slow.Digest,
		err:     boom,
		started: make(chan struct{}),
	}
	held := holdingFetcher{blobStore: src, hold: arm64.Digest, until: dst.started}

	err := mustNotHang(t, func() error {
		return Copy(context.Background(), dst, held, index, WithConcurrency(8))
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Copy error = %v, want the underlying %v", err, boom)
	}
}

// TestCopy_CancelledContextStops checks an already-cancelled context aborts
// before anything is transferred, so Ctrl-C does not keep uploading.
func TestCopy_CancelledContextStops(t *testing.T) {
	src := newBlobStore()
	manifest := src.addManifest(t, "app", src.addLayers("app", 4))
	dst := newGatedPusher(0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Copy(ctx, dst, src, manifest, WithConcurrency(4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Copy error = %v, want context.Canceled", err)
	}
	if got := len(dst.order()); got != 0 {
		t.Errorf("pushed %d blobs after cancellation, want 0", got)
	}
}

// TestCopy_DefaultsToParallel confirms callers that pass no options still get
// concurrent transfers rather than silently falling back to one at a time.
func TestCopy_DefaultsToParallel(t *testing.T) {
	src := newBlobStore()
	manifest := src.addManifest(t, "app", src.addLayers("app", 2*DefaultConcurrency))
	dst := newGatedPusher(DefaultConcurrency)

	if err := Copy(context.Background(), dst, src, manifest); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if got := dst.highWater(); got != DefaultConcurrency {
		t.Errorf("peak concurrent pushes = %d, want DefaultConcurrency %d", got, DefaultConcurrency)
	}
}

// TestCopy_ClampsNonPositiveConcurrency keeps a bad value from wedging the copy
// on a zero-capacity semaphore.
func TestCopy_ClampsNonPositiveConcurrency(t *testing.T) {
	for _, n := range []int{0, -1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			src := newBlobStore()
			manifest := src.addManifest(t, "app", src.addLayers("app", 3))
			dst := newGatedPusher(0)

			if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(n)); err != nil {
				t.Fatalf("Copy: %v", err)
			}
			if got := len(dst.order()); got != 5 {
				t.Errorf("pushed %d blobs, want 5", got)
			}
		})
	}
}

// TestCopy_RejectsContentThatDoesNotMatchItsDigest pins the property the rest of
// the copier leans on. Which children get walked is decided by what the source
// returns, and copy has one goroutine wait on another's result — safe only while
// every reference really is a content address, so a source that answers a digest
// with different bytes has to be refused rather than followed.
func TestCopy_RejectsContentThatDoesNotMatchItsDigest(t *testing.T) {
	src := newBlobStore()
	honest := src.addManifest(t, "app", src.addLayers("app", 2))

	// Same digest, different bytes.
	tampered := src.addManifest(t, "evil", src.addLayers("evil", 1))
	src.blobs[honest.Digest.String()] = src.blobs[tampered.Digest.String()]

	dst := newGatedPusher(0)
	err := Copy(context.Background(), dst, src, honest, WithConcurrency(4))
	if err == nil {
		t.Fatal("Copy accepted a manifest whose content does not hash to its digest")
	}
	if !strings.Contains(err.Error(), "does not match its digest") {
		t.Errorf("Copy error = %v, want it to name the digest mismatch", err)
	}
	if len(dst.order()) != 0 {
		t.Errorf("pushed %d blobs from a source that answered with the wrong content", len(dst.order()))
	}
}

// TestAncestors_Contains covers the loop backstop directly. Digest verification
// means the walk never actually reaches it, so nothing else in the suite does.
func TestAncestors_Contains(t *testing.T) {
	first, second := digest.FromString("first"), digest.FromString("second")

	var path *ancestors
	if path.contains(first) {
		t.Error("an empty path contains nothing")
	}

	path = path.push(first).push(second)
	if !path.contains(first) || !path.contains(second) {
		t.Error("a pushed digest must be found again")
	}
	if path.contains(digest.FromString("third")) {
		t.Error("a digest that was never pushed must not be found")
	}

	// Branches must not see each other's descendants.
	if path.push(digest.FromString("left")).parent.contains(digest.FromString("left")) {
		t.Error("push mutated the path it was called on")
	}
}

// mustNotHang runs a copy on its own goroutine and fails the test if it does not
// come back, so a reference loop shows up as a failure instead of a stuck suite.
func mustNotHang(t *testing.T, copy func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- copy() }()

	select {
	case err := <-done:
		return err
	case <-time.After(gateTimeout):
		t.Fatal("Copy never returned — a reference loop parked a goroutine on its own task")
		return nil
	}
}

// TestCopy_SelfReferencingManifestFails covers a source that lies about its own
// content. Content addressing rules out a real loop, so the only way to get one
// is a registry that answers a digest with something else — here, a manifest
// whose subject is that same manifest. Copy must reject it, because the
// goroutine that owns that digest would otherwise wait for itself.
func TestCopy_SelfReferencingManifestFails(t *testing.T) {
	src := newBlobStore()
	self := ocispecv1.Descriptor{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Digest:    digest.FromString("self-referencing-manifest"),
	}
	data, err := json.Marshal(ocispecv1.Manifest{
		MediaType: ocispecv1.MediaTypeImageManifest,
		Config:    src.add(ocispecv1.MediaTypeImageConfig, []byte("config")),
		Subject:   &self,
	})
	if err != nil {
		t.Fatal(err)
	}
	src.blobs[self.Digest.String()] = data

	dst := newGatedPusher(0)
	err = mustNotHang(t, func() error {
		return Copy(context.Background(), dst, src, self, WithConcurrency(4))
	})
	if err == nil {
		t.Fatal("Copy of a self-referencing manifest should fail")
	}
	if slices.Contains(dst.order(), self.Digest) {
		t.Error("the looping manifest was pushed anyway")
	}
}

// TestCopy_MutuallyReferencingManifestsFail is the same hazard one step removed:
// two manifests naming each other as their subject.
func TestCopy_MutuallyReferencingManifestsFail(t *testing.T) {
	src := newBlobStore()
	first := ocispecv1.Descriptor{MediaType: ocispecv1.MediaTypeImageManifest, Digest: digest.FromString("first")}
	second := ocispecv1.Descriptor{MediaType: ocispecv1.MediaTypeImageManifest, Digest: digest.FromString("second")}
	config := src.add(ocispecv1.MediaTypeImageConfig, []byte("config"))

	for _, pair := range [][2]ocispecv1.Descriptor{{first, second}, {second, first}} {
		desc, subject := pair[0], pair[1]
		data, err := json.Marshal(ocispecv1.Manifest{
			MediaType: ocispecv1.MediaTypeImageManifest,
			Config:    config,
			Subject:   &subject,
		})
		if err != nil {
			t.Fatal(err)
		}
		src.blobs[desc.Digest.String()] = data
	}

	err := mustNotHang(t, func() error {
		return Copy(context.Background(), newGatedPusher(0), src, first, WithConcurrency(4))
	})
	if err == nil {
		t.Fatal("Copy of mutually referencing manifests should fail")
	}
}

// TestCopy_CrossBranchLoopFails is the loop that hides from a plain
// already-visited check: an index whose two branches lead into each other, so no
// single root-to-node path repeats a digest until the branches cross. Walking
// both branches concurrently, each one has to notice the loop on its own.
//
//	index ─┬─> alpha ──> bridge ──┐
//	       └─> beta <─────────────┘
//	           beta ──> alpha
func TestCopy_CrossBranchLoopFails(t *testing.T) {
	src := newBlobStore()
	config := src.add(ocispecv1.MediaTypeImageConfig, []byte("config"))

	manifestDesc := func(name string) ocispecv1.Descriptor {
		return ocispecv1.Descriptor{MediaType: ocispecv1.MediaTypeImageManifest, Digest: digest.FromString(name)}
	}
	alpha, beta, bridge := manifestDesc("alpha"), manifestDesc("beta"), manifestDesc("bridge")

	for _, pair := range [][2]ocispecv1.Descriptor{{alpha, bridge}, {bridge, beta}, {beta, alpha}} {
		desc, subject := pair[0], pair[1]
		data, err := json.Marshal(ocispecv1.Manifest{
			MediaType: ocispecv1.MediaTypeImageManifest,
			Config:    config,
			Subject:   &subject,
		})
		if err != nil {
			t.Fatal(err)
		}
		src.blobs[desc.Digest.String()] = data
	}

	index := src.addJSON(t, ocispecv1.MediaTypeImageIndex, ocispecv1.Index{
		MediaType: ocispecv1.MediaTypeImageIndex,
		Manifests: []ocispecv1.Descriptor{alpha, beta},
	})

	err := mustNotHang(t, func() error {
		return Copy(context.Background(), newGatedPusher(0), src, index, WithConcurrency(4))
	})
	if err == nil {
		t.Fatal("Copy of an index whose branches loop into each other should fail")
	}
}

// TestCopy_AlreadyExistsIsNotAnError keeps the "destination already has it"
// shortcut working now that pushes race each other.
func TestCopy_AlreadyExistsIsNotAnError(t *testing.T) {
	src := newBlobStore()
	layers := src.addLayers("app", 4)
	manifest := src.addManifest(t, "app", layers)

	dst := newGatedPusher(0)
	dst.failOn, dst.err = layers[1].Digest, ErrAlreadyExists

	if err := Copy(context.Background(), dst, src, manifest, WithConcurrency(4)); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !slices.Contains(dst.order(), manifest.Digest) {
		t.Error("manifest not pushed although the only failure was ErrAlreadyExists")
	}
}
