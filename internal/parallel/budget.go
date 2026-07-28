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

// Package parallel bounds how much of one operation runs at once.
//
// Copying an image and packing an artifact are the same shape of problem: a tree
// of content-addressed blobs, moved over a network that is the bottleneck, where
// the same digest can be reached more than once and the answer to a failure is
// to stop the rest. Budget is the piece both of them share.
package parallel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
)

// DefaultConcurrency is how much runs at once when the caller does not ask for a
// number. This work waits on the network far more than on the CPU, so a few
// transfers in flight beat one by a wide margin, while staying polite to the
// registry at the other end.
const DefaultConcurrency = 4

// Budget is the concurrency budget of a single operation: the slots that cap how
// many transfers are in flight, the keys already claimed so no work is done
// twice, and the first real failure so it can be reported instead of the
// cancellation it set off.
//
// The zero value is not usable; call NewBudget. A Budget is safe for concurrent
// use and is meant to be shared by every phase of one operation, so that the
// limit is a ceiling on the whole thing rather than on each part of it.
type Budget struct {
	limit int
	slots chan struct{}

	mu      sync.Mutex
	tasks   map[digest.Digest]*task
	failure error
}

// task is the shared outcome of one claimed key. Whoever claimed it closes done
// when finished; everyone else asking for the same key waits on it and reuses
// err.
type task struct {
	done chan struct{}
	err  error
}

// NewBudget returns a budget that allows limit transfers at a time. Values below
// 1 are clamped to 1, which runs everything in order in the caller's goroutine.
func NewBudget(limit int) *Budget {
	if limit < 1 {
		limit = 1
	}

	return &Budget{
		limit: limit,
		slots: make(chan struct{}, limit),
		tasks: make(map[digest.Digest]*task),
	}
}

// Limit is the number of transfers allowed at a time.
func (b *Budget) Limit() int { return b.limit }

// Each runs fn for every index in [0, n) and returns once they have all
// finished; the first failure cancels the rest. At a limit of 1 they run in
// order in the caller's goroutine, so a sequential run behaves exactly as it did
// before any of this ran in parallel.
//
// Callers fan out over positions rather than values so results can be written
// straight into the slot they belong in — layers keep their order however the
// transfers happen to finish.
//
// Each does not take a slot, and neither does the goroutine waiting on it. Only
// Slot does.
func (b *Budget) Each(ctx context.Context, n int, fn func(context.Context, int) error) error {
	if b.limit < 2 || n < 2 {
		for i := range n {
			if err := b.Record(fn(ctx, i)); err != nil {
				return err
			}
		}
		return nil
	}

	group, groupCtx := errgroup.WithContext(ctx)
	for i := range n {
		group.Go(func() error { return b.Record(fn(groupCtx, i)) })
	}

	return group.Wait()
}

// Slot runs fn holding one of the slots, for as long as the bytes are moving.
//
// Only the transfers themselves may take a slot, and never while waiting on
// something else: a goroutine waiting on the work it fanned out, or on a key
// another goroutine claimed, must hold none. Otherwise the slots can all end up
// held by waiters with nobody left able to make progress.
func (b *Budget) Slot(ctx context.Context, fn func() error) error {
	// A free slot and a cancelled context are both ready cases below, and select
	// would pick between them at random; cancellation has to win.
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case b.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-b.slots }()

	return fn()
}

// Once runs fn at most one time per key. Whoever gets to a key first does the
// work and everyone else waits for that result and shares it — content is
// addressed by digest, so the same key twice is the same bytes twice: wasted
// upload against a registry, and two writers on one file against a local layout.
//
// Once does not take a slot itself, which leaves two safe ways to use it, and
// the caller has to pick one:
//
//   - Call it with no slot held, as a walk does. Waiters then hold nothing, and
//     the claimer is free to take slots as it goes.
//   - Call it from inside Slot, as an upload does. The claimer then already holds
//     a slot and can always finish, so waiters holding the rest is harmless.
//
// What is not safe is waiting on a key whose claimer has yet to acquire a slot
// while holding one yourself.
//
// Waiting also assumes the work cannot lead back to itself. For a graph of
// verified content addresses that holds; where it might not, the caller has to
// rule loops out before getting here.
func (b *Budget) Once(ctx context.Context, key digest.Digest, fn func() error) error {
	b.mu.Lock()
	claimed, found := b.tasks[key]
	if !found {
		claimed = &task{done: make(chan struct{})}
		b.tasks[key] = claimed
	}
	b.mu.Unlock()

	if found {
		select {
		case <-claimed.done:
			return claimed.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Overwritten on the normal return below. The placeholder only survives an
	// abnormal unwind — a panic, a test double calling t.Fatal — where waiters
	// would otherwise sit on a channel nobody is left to close.
	claimed.err = fmt.Errorf("work on '%s' did not complete", key)
	defer close(claimed.done)

	claimed.err = b.Record(fn())

	return claimed.err
}

// Record remembers the first genuine failure, so that Cause can report it rather
// than the cancellation it set off. Context errors are skipped: they are what
// everything else returns once the run is being torn down, never the reason it is
// being torn down.
//
// It returns err untouched, so it can wrap a call in place.
func (b *Budget) Record(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure == nil {
		b.failure = err
	}

	return err
}

// Cause returns what to report for a run that ended in err: the failure that
// actually stopped it, if one was seen, and otherwise err itself.
func (b *Budget) Cause(err error) error {
	if err == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return b.failure
	}

	return err
}
