package mcpserver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingFetch returns a fetch function that counts calls and blocks until
// release is closed.
func blockingFetch(calls *atomic.Int32, release <-chan struct{}) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		calls.Add(1)

		select {
		case <-release:
			return "value", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func TestCacheConcurrentCallersShareOneFetch(t *testing.T) {
	var calls atomic.Int32

	release := make(chan struct{})
	c := newCache(time.Minute, time.Now, blockingFetch(&calls, release))

	const n = 10

	var wg sync.WaitGroup

	results := make(chan string, n)
	for range n {
		wg.Go(func() {
			v, err := c.get(context.Background())
			if err != nil {
				t.Error(err)
			}

			results <- v
		})
	}
	// Let every caller reach the shared fetch before releasing it.
	waitFor(t, func() bool { return calls.Load() == 1 })
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	for v := range results {
		if v != "value" {
			t.Errorf("got %q", v)
		}
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("fetch called %d times, want 1", n)
	}
}

func TestCacheWaitingCallerCanGiveUp(t *testing.T) {
	var calls atomic.Int32

	release := make(chan struct{})
	c := newCache(time.Minute, time.Now, blockingFetch(&calls, release))

	// The first caller starts the fetch and keeps waiting.
	first := make(chan error, 1)

	go func() {
		_, err := c.get(context.Background())
		first <- err
	}()

	waitFor(t, func() bool { return calls.Load() == 1 })

	// A second caller gives up while the fetch is still in flight.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()

	if _, err := c.get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}

	if waited := time.Since(start); waited > time.Second {
		t.Errorf("cancelled caller waited %v", waited)
	}

	close(release)

	if err := <-first; err != nil {
		t.Errorf("first caller: %v", err)
	}
}

func TestCacheFetchSurvivesInitiatorCancellation(t *testing.T) {
	var calls atomic.Int32

	release := make(chan struct{})
	c := newCache(time.Minute, time.Now, blockingFetch(&calls, release))

	// The caller that starts the fetch gives up immediately...
	ctx, cancel := context.WithCancel(context.Background())
	initiator := make(chan error, 1)

	go func() {
		_, err := c.get(ctx)
		initiator <- err
	}()

	waitFor(t, func() bool { return calls.Load() == 1 })
	cancel()

	if err := <-initiator; !errors.Is(err, context.Canceled) {
		t.Fatalf("initiator err = %v, want context.Canceled", err)
	}

	// ...but the fetch carries on, and its result is shared and cached.
	other := make(chan string, 1)

	go func() {
		v, _ := c.get(context.Background())
		other <- v
	}()

	close(release)

	if v := <-other; v != "value" {
		t.Errorf("other caller got %q", v)
	}

	if v, err := c.get(context.Background()); err != nil || v != "value" {
		t.Errorf("cached get = %q, %v", v, err)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("fetch called %d times, want 1", n)
	}
}

func TestCacheFetchIsBounded(t *testing.T) {
	var deadline time.Time

	c := newCache(time.Minute, time.Now, func(ctx context.Context) (string, error) {
		deadline, _ = ctx.Deadline()
		return "value", nil
	})
	// The caller has no deadline, but the detached fetch still gets one.
	if _, err := c.get(context.Background()); err != nil {
		t.Fatal(err)
	}

	if deadline.IsZero() || time.Until(deadline) > maxFetchTime {
		t.Errorf("fetch deadline = %v, want within %v", deadline, maxFetchTime)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}

		time.Sleep(time.Millisecond)
	}
}
