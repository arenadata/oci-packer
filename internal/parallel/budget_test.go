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

package parallel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
)

func digestOf(s string) digest.Digest { return digest.FromString(s) }

// gate blocks everything that reaches it until `want` callers have arrived, so a
// test observes real overlap rather than inferring it from timings. It gives up
// after a deadline, in which case the test fails on the peak it measured instead
// of hanging.
type gate struct {
	want     int
	deadline time.Time
	reached  chan struct{}
	open     sync.Once

	mu       sync.Mutex
	inFlight int
	peak     int
}

func newGate(want int) *gate {
	return &gate{want: want, deadline: time.Now().Add(2 * time.Second), reached: make(chan struct{})}
}

func (g *gate) enter() {
	g.mu.Lock()
	g.inFlight++
	g.peak = max(g.peak, g.inFlight)
	arrived := g.inFlight
	g.mu.Unlock()

	if g.want > 0 {
		if arrived >= g.want {
			g.open.Do(func() { close(g.reached) })
		}
		timer := time.NewTimer(time.Until(g.deadline))
		select {
		case <-g.reached:
		case <-timer.C:
		}
		timer.Stop()
	}

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
}

func (g *gate) highWater() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

func TestSlot_RunsUpToTheLimitAtOnce(t *testing.T) {
	const limit = 4

	b := NewBudget(limit)
	g := newGate(limit)

	if err := b.Each(context.Background(), 12, func(ctx context.Context, _ int) error {
		return b.Slot(ctx, func() error { g.enter(); return nil })
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}

	if got := g.highWater(); got != limit {
		t.Errorf("peak concurrent slots = %d, want exactly %d "+
			"(below means nothing overlapped, above means the limit leaks)", got, limit)
	}
}

func TestEach_RunsInOrderAtLimitOne(t *testing.T) {
	b := NewBudget(1)

	var seen []int
	if err := b.Each(context.Background(), 5, func(_ context.Context, i int) error {
		seen = append(seen, i)
		return nil
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}

	if !slices.Equal(seen, []int{0, 1, 2, 3, 4}) {
		t.Errorf("ran %v, want them in order", seen)
	}
}

func TestEach_FirstFailureCancelsTheRest(t *testing.T) {
	boom := errors.New("no")
	b := NewBudget(4)

	var mu sync.Mutex
	var cancelled int

	err := b.Each(context.Background(), 8, func(ctx context.Context, i int) error {
		if i == 0 {
			return boom
		}
		// Everything else waits to be told to stop.
		<-ctx.Done()
		mu.Lock()
		cancelled++
		mu.Unlock()
		return ctx.Err()
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Each error = %v, want %v", err, boom)
	}

	mu.Lock()
	defer mu.Unlock()
	if cancelled != 7 {
		t.Errorf("%d of the other 7 were cancelled, want all of them", cancelled)
	}
}

func TestOnce_RunsOncePerKeyAndSharesTheResult(t *testing.T) {
	b := NewBudget(8)
	key := digestOf("shared")

	var mu sync.Mutex
	runs := 0

	// Every caller blocks inside fn until they have all arrived, so they are
	// genuinely concurrent and a second run would be observed.
	g := newGate(1)
	if err := b.Each(context.Background(), 8, func(ctx context.Context, _ int) error {
		return b.Once(ctx, key, func() error {
			mu.Lock()
			runs++
			mu.Unlock()
			g.enter()
			return nil
		})
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("fn ran %d times, want exactly 1", runs)
	}
}

func TestOnce_SharesTheFailureToo(t *testing.T) {
	boom := errors.New("upload rejected")
	b := NewBudget(1)
	key := digestOf("shared")

	first := b.Once(context.Background(), key, func() error { return boom })
	second := b.Once(context.Background(), key, func() error {
		t.Error("fn ran again after it had already failed")
		return nil
	})

	if !errors.Is(first, boom) || !errors.Is(second, boom) {
		t.Errorf("got %v and %v, want both to be %v", first, second, boom)
	}
}

func TestOnce_CancelledWaiterDoesNotBlock(t *testing.T) {
	b := NewBudget(2)
	key := digestOf("slow")

	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- b.Once(context.Background(), key, func() error { <-release; return nil })
	}()

	// Wait until the key is claimed, then join it with a context of our own.
	for {
		b.mu.Lock()
		claimed := len(b.tasks) > 0
		b.mu.Unlock()
		if claimed {
			break
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Once(ctx, key, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("waiter error = %v, want context.Canceled", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Errorf("claimer error = %v, want nil", err)
	}
}

func TestRecord_ReportsTheRealFailureNotTheFallout(t *testing.T) {
	boom := errors.New("registry rejected the upload")
	b := NewBudget(4)

	b.Record(boom)
	b.Record(context.Canceled)
	b.Record(errors.New("a later, unrelated problem"))

	if got := b.Cause(context.Canceled); !errors.Is(got, boom) {
		t.Errorf("Cause = %v, want the first real failure %v", got, boom)
	}
	if got := b.Cause(nil); got != nil {
		t.Errorf("Cause of a run that succeeded = %v, want nil", got)
	}
}

func TestRecord_ContextErrorsAreNotFailures(t *testing.T) {
	b := NewBudget(4)

	b.Record(context.Canceled)
	b.Record(context.DeadlineExceeded)

	if got := b.Cause(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Errorf("Cause = %v, want the context error itself when nothing else went wrong", got)
	}
}

func TestNewBudget_ClampsNonPositiveLimit(t *testing.T) {
	for _, n := range []int{0, -1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			b := NewBudget(n)
			if b.Limit() != 1 {
				t.Fatalf("Limit = %d, want 1", b.Limit())
			}
			// A zero-capacity semaphore would wedge here.
			if err := b.Slot(context.Background(), func() error { return nil }); err != nil {
				t.Errorf("Slot: %v", err)
			}
		})
	}
}
