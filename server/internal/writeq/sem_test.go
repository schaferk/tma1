package writeq

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemAcceptsUpToCapacity(t *testing.T) {
	s := New(2)
	release := make(chan struct{})
	var ran atomic.Int32
	for i := 0; i < 2; i++ {
		if !s.Go(func() {
			ran.Add(1)
			<-release
		}) {
			t.Fatalf("expected job %d accepted", i)
		}
	}
	// Third must drop — both slots in use.
	if s.Go(func() { ran.Add(1) }) {
		t.Error("expected drop when full")
	}
	if got := s.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d, want 1", got)
	}

	close(release)
	// Give the released goroutines a moment to finish before re-checking.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ran.Load() == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ran.Load() != 2 {
		t.Errorf("ran = %d, want 2", ran.Load())
	}

	// After draining, new jobs should be accepted again.
	if !s.Go(func() { ran.Add(1) }) {
		t.Error("expected acceptance after drain")
	}
}

func TestSemReleasesSlotOnPanic(t *testing.T) {
	// A panicking job must not poison the slot. We don't promise to
	// recover the panic ourselves (that's the caller's job), but the
	// deferred slot release must run regardless.
	s := New(1)
	done := make(chan struct{})
	if !s.Go(func() {
		defer close(done)
		defer func() { _ = recover() }()
		panic("boom")
	}) {
		t.Fatal("expected initial Go to succeed")
	}
	<-done
	// Slot should now be free.
	var wg sync.WaitGroup
	wg.Add(1)
	if !s.Go(func() { defer wg.Done() }) {
		t.Fatal("slot not released after panic")
	}
	wg.Wait()
}

func TestSemRejectsNil(t *testing.T) {
	s := New(4)
	if s.Go(nil) {
		t.Error("nil fn should be rejected")
	}
}
