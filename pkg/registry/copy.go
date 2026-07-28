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
	"sort"
	"sync"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// DefaultConcurrency is how many blobs Copy transfers at once when the caller
// does not ask for a specific number. Layer transfers are dominated by network
// and disk latency, so a few in flight saturate a link far better than one while
// staying polite to the registry.
const DefaultConcurrency = 4

// CopyOption configures a Copy call.
type CopyOption func(*copyOptions)

type copyOptions struct {
	concurrency int
}

// WithConcurrency sets how many blobs — layers, configs, manifests — Copy
// transfers simultaneously. Values below 1 are clamped to 1, which walks the
// image strictly sequentially and in order, as copying did before parallelism.
func WithConcurrency(n int) CopyOption {
	return func(o *copyOptions) { o.concurrency = n }
}

// Copy transfers the artifact described by desc, and everything it references,
// from src to dst. Children are always copied before their parent, so a manifest
// or index only lands once every blob it points at is present in dst.
//
// The work runs in parallel, DefaultConcurrency at a time unless WithConcurrency
// says otherwise, and that number is the ceiling on everything asked of the two
// endpoints — reading a manifest to walk it counts against it as much as moving a
// layer does.
//
// Each digest is copied at most once per call, however many routes lead to it, so
// a layer shared by several manifests of a multi-platform index costs one copy
// rather than one per manifest.
//
// The first failure cancels the rest and is what Copy reports. Blobs that already
// landed stay where they are, so running the copy again resumes it.
func Copy(ctx context.Context, dst Pusher, src Fetcher, desc ocispecv1.Descriptor, opts ...CopyOption) error {
	options := copyOptions{concurrency: DefaultConcurrency}
	for _, opt := range opts {
		opt(&options)
	}
	if options.concurrency < 1 {
		options.concurrency = 1
	}

	c := &copier{
		dst:   dst,
		src:   src,
		limit: options.concurrency,
		slots: make(chan struct{}, options.concurrency),
		tasks: make(map[digest.Digest]*copyTask),
	}

	err := c.copy(ctx, desc, nil)
	if err == nil {
		return nil
	}

	// Report what actually went wrong. The first failure cancels every other
	// transfer in flight, so by the time the error unwinds there are several
	// context.Canceled results racing it, and whichever one an errgroup happened
	// to see first would otherwise be all the user is told.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return c.failure
	}

	return err
}

// ancestors is the chain of descriptors being walked above the current one, kept
// so that a reference loop is caught rather than followed.
//
// It is a backstop. fetch already refuses content that does not hash to the
// digest it was asked for, which is what rules loops out to begin with — so in
// practice this never fires. It is here because copy's deduplication has one
// goroutine wait on another's result, and if the no-loops property were ever
// weakened the failure would be a silent hang rather than an error.
//
// It is a linked list, not a set: chains are short (index → manifest → subject)
// and every branch of the walk needs its own view of the path above it.
type ancestors struct {
	digest digest.Digest
	parent *ancestors
}

func (a *ancestors) push(dgst digest.Digest) *ancestors {
	return &ancestors{digest: dgst, parent: a}
}

func (a *ancestors) contains(dgst digest.Digest) bool {
	for node := a; node != nil; node = node.parent {
		if node.digest == dgst {
			return true
		}
	}
	return false
}

// copyTask is the shared outcome of copying one descriptor and everything under
// it. The goroutine that claimed the digest closes done when it has finished;
// everyone else asking for the same digest waits on it and reuses err.
type copyTask struct {
	done chan struct{}
	err  error
}

// copier holds the state of a single Copy call: the two endpoints, the slots
// that cap parallelism, and the table of digests already claimed.
type copier struct {
	dst   Pusher
	src   Fetcher
	limit int
	slots chan struct{}

	mu      sync.Mutex
	tasks   map[digest.Digest]*copyTask
	failure error
}

// record remembers the first genuine failure of the run, so Copy can report it
// instead of the cancellation fallout it triggered. Context errors are skipped:
// they are what every other transfer returns once the run is being torn down,
// never the reason it is being torn down.
func (c *copier) record(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil {
		c.failure = err
	}

	return err
}

// copy copies desc and everything it references, at most once per digest.
// Whoever asks for a digest first owns it; anyone else asking for the same one
// waits for that result instead of walking the same subtree again. Without that,
// an index whose children share a descriptor walks it once per route to it,
// which is exponential in the nesting depth — a few tens of kilobytes of
// manifests can then cost gigabytes of live goroutines.
//
// Waiting on another goroutine's work is safe because the descriptor graph
// cannot contain a loop: every reference is a digest and fetch checks that what
// came back really hashes to it, so a loop would need a hash cycle. Every wait
// therefore points from a node to one of its children, and a cycle of waits
// would have to be a cycle in the graph itself.
func (c *copier) copy(ctx context.Context, desc ocispecv1.Descriptor, path *ancestors) error {
	// Bail early: once a sibling has failed the group context is cancelled, and
	// there is nothing to gain from work that is only going to be thrown away.
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	task, claimed := c.tasks[desc.Digest]
	if !claimed {
		task = &copyTask{done: make(chan struct{})}
		c.tasks[desc.Digest] = task
	}
	c.mu.Unlock()

	if claimed {
		select {
		case <-task.done:
			return task.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Overwritten on every normal return below. The placeholder only survives an
	// abnormal unwind — a Pusher that panics, a test double calling t.Fatal —
	// where waiters would otherwise sit on a channel nobody is left to close.
	task.err = fmt.Errorf("copy of '%s' did not complete", desc.Digest)
	defer close(task.done)

	task.err = c.record(c.walk(ctx, desc, path))

	return task.err
}

// walk copies everything desc references and only then desc itself. It is split
// out of copy so that every failure anywhere in the tree passes through record
// on its way up.
func (c *copier) walk(ctx context.Context, desc ocispecv1.Descriptor, path *ancestors) error {
	if path.contains(desc.Digest) {
		return fmt.Errorf("descriptor '%s' is reachable from itself", desc.Digest)
	}

	fields := map[string]any{"digest": desc.Digest, "size": desc.Size, "media_type": desc.MediaType}
	log := logger.New("copy")
	log.WithFields(fields).Debug("copy artifact by descriptor")

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := c.fetchJSON(ctx, desc, &index); err != nil {
			return err
		}
		if err := c.copyAll(ctx, index.Manifests, path.push(desc.Digest)); err != nil {
			return err
		}

	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispecv1.Manifest
		if err := c.fetchJSON(ctx, desc, &manifest); err != nil {
			return err
		}
		if err := c.copyAll(ctx, manifestChildren(manifest), path.push(desc.Digest)); err != nil {
			return err
		}
	}

	return c.transferBlob(ctx, desc)
}

// fetchJSON reads a manifest or index from the source. It takes a slot for the
// request, so an index with thirty children opens -j connections rather than
// thirty: the flag caps everything this copy asks of the endpoints, not just the
// bytes of the layers.
func (c *copier) fetchJSON(ctx context.Context, desc ocispecv1.Descriptor, v any) error {
	return c.withSlot(ctx, func() error { return fetch(ctx, c.src, desc, v) })
}

// manifestChildren lists everything a manifest references, in the order the
// sequential copier walked them: config, then layers, then the referrers
// subject.
func manifestChildren(manifest ocispecv1.Manifest) []ocispecv1.Descriptor {
	children := make([]ocispecv1.Descriptor, 0, len(manifest.Layers)+2)
	children = append(children, manifest.Config)
	children = append(children, manifest.Layers...)
	if manifest.Subject != nil {
		children = append(children, *manifest.Subject)
	}

	return children
}

// copyAll copies a set of sibling descriptors and returns once every one of them
// has landed in the destination. The first failure cancels the rest.
func (c *copier) copyAll(ctx context.Context, descs []ocispecv1.Descriptor, path *ancestors) error {
	if c.limit < 2 || len(descs) < 2 {
		for _, desc := range descs {
			if err := c.copy(ctx, desc, path); err != nil {
				return err
			}
		}
		return nil
	}

	group, groupCtx := errgroup.WithContext(ctx)
	for _, desc := range descs {
		group.Go(func() error { return c.copy(groupCtx, desc, path) })
	}

	return group.Wait()
}

// transferBlob moves one blob, holding a slot for as long as the bytes are
// flowing.
func (c *copier) transferBlob(ctx context.Context, desc ocispecv1.Descriptor) error {
	return c.withSlot(ctx, func() error { return copyDescriptor(ctx, c.dst, c.src, desc) })
}

// withSlot runs fn holding one of the -j slots. Only talking to the endpoints
// takes a slot, and never while waiting on something else: a goroutine waiting
// on its children, or on a transfer another goroutine owns, holds none. So
// however wide or deep the tree gets, the slots cannot all be occupied by
// waiters and stall the copy.
func (c *copier) withSlot(ctx context.Context, fn func() error) error {
	// A free slot and a cancelled context are both ready cases below, and select
	// would pick between them at random; cancellation has to win.
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.slots }()

	return fn()
}

// SelectPlatform resolves an image to a single platform. If desc is an OCI
// Index (multi-platform), it returns the child manifest descriptor that best
// matches the given platform, erroring if none match. If desc is already a
// single manifest, it is returned unchanged.
func SelectPlatform(ctx context.Context, src Fetcher, desc ocispecv1.Descriptor, match platforms.MatchComparer) (ocispecv1.Descriptor, error) {
	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := fetch(ctx, src, desc, &index); err != nil {
			return ocispecv1.Descriptor{}, err
		}

		var matched []ocispecv1.Descriptor
		for _, m := range index.Manifests {
			if m.Platform != nil && match.Match(*m.Platform) {
				matched = append(matched, m)
			}
		}
		if len(matched) == 0 {
			return ocispecv1.Descriptor{}, fmt.Errorf("no manifest in index matches the requested platform")
		}

		sort.SliceStable(matched, func(i, j int) bool {
			return match.Less(*matched[i].Platform, *matched[j].Platform)
		})
		return matched[0], nil
	}

	return desc, nil
}

func copyDescriptor(ctx context.Context, dst Pusher, src Fetcher, desc ocispecv1.Descriptor) error {
	fields := map[string]any{"digest": desc.Digest, "media_type": desc.MediaType}
	log := logger.New("copy_descriptor")
	log.WithFields(fields).Debug("copying artifact")

	ref := reference.Reference{Ref: desc.Digest.String()}
	r, err := src.Fetch(ctx, ref.WithDescriptor(desc))
	if err != nil {
		log.WithError(err).WithFields(fields).Error("error fetching descriptor")
		return err
	}
	defer func() { _ = r.Close() }()

	log.WithFields(fields).Info("copy artifact")
	if err = dst.Push(ctx, desc, r); err != nil {
		if !IsAlreadyExists(err) {
			return err
		}
		log.WithFields(fields).Info("file already exists")
	}
	return nil
}

// maxManifestSize caps how much of a manifest or index will be read into memory.
// The distribution spec puts the ceiling at 4 MiB; anything larger is a broken
// or hostile source, and reading it in is what such a source is hoping for.
const maxManifestSize = 4 << 20

// fetch decodes the JSON document desc points at, after checking that the bytes
// really do hash to desc.Digest.
//
// The descriptor travels with the reference because a digest alone does not say
// whether it names a manifest or a blob, and a registry serves the two from
// different endpoints.
//
// The digest check is not just hygiene. What comes back here decides which
// children get walked, and Copy relies on those references forming an acyclic
// graph — which holds only as long as every reference really is a content
// address. A source free to answer with whatever it likes could otherwise hand
// back a graph that loops, and one goroutine would end up waiting for itself.
func fetch(ctx context.Context, src Fetcher, desc ocispecv1.Descriptor, v any) error {
	algorithm := desc.Digest.Algorithm()
	if !algorithm.Available() {
		return fmt.Errorf("unsupported digest algorithm '%s' for '%s'", algorithm, desc.Digest)
	}

	ref := reference.Reference{Ref: desc.Digest.String()}

	reader, err := src.Fetch(ctx, ref.WithDescriptor(desc))
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(io.LimitReader(reader, maxManifestSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxManifestSize {
		return fmt.Errorf("'%s' is larger than the %d byte manifest limit", desc.Digest, maxManifestSize)
	}

	if actual := algorithm.FromBytes(data); actual != desc.Digest {
		return fmt.Errorf("content of '%s' does not match its digest (got %s)", desc.Digest, actual)
	}

	return json.Unmarshal(data, v)
}
