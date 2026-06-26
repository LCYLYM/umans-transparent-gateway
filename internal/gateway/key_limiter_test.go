package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyLimiterQueuesFifthRequestForSameKey(t *testing.T) {
	limiter, err := NewKeyLimiter(4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var releases []func()
	for i := 0; i < 4; i++ {
		release, err := limiter.Acquire(ctx, "sk-same")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	acquired := make(chan func(), 1)
	errCh := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(ctx, "sk-same")
		if err != nil {
			errCh <- err
			return
		}
		acquired <- release
	}()

	select {
	case <-acquired:
		t.Fatal("fifth same-key request acquired before a slot was released")
	case err := <-errCh:
		t.Fatalf("fifth same-key request failed early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	releases[0]()
	select {
	case release := <-acquired:
		release()
	case err := <-errCh:
		t.Fatalf("queued acquire failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("queued acquire did not resume after release")
	}
	for _, release := range releases[1:] {
		release()
	}
}

func TestKeyLimiterSeparatesDifferentKeys(t *testing.T) {
	limiter, err := NewKeyLimiter(1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	releaseA, err := limiter.Acquire(context.Background(), "sk-a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()

	releaseB, err := limiter.Acquire(context.Background(), "sk-b")
	if err != nil {
		t.Fatalf("different key should not wait behind sk-a: %v", err)
	}
	releaseB()
}

func TestKeyLimiterQueueTimeout(t *testing.T) {
	limiter, err := NewKeyLimiter(1, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	release, err := limiter.Acquire(context.Background(), "sk-same")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, err = limiter.Acquire(context.Background(), "sk-same")
	if !errors.Is(err, ErrConcurrencyQueueTimeout) {
		t.Fatalf("err=%v, want queue timeout", err)
	}
}
