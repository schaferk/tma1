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
	// "Slot released" happens AFTER the user fn returns, inside Sem.Go's
	// deferred <-s.ch. Waiting on ran==2 only proves both fns started,
	// not that the deferred release fired -- so a naive Go() right after
	// that check races the release and can falsely report "expected
	// acceptance after drain". Poll-retry instead: keep trying to enqueue
	// a fresh job until the semaphore actually has room.
	deadline := time.Now().Add(time.Second)
	accepted := false
	for time.Now().Before(deadline) {
		if s.Go(func() { ran.Add(1) }) {
			accepted = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !accepted {
		t.Error("never accepted after drain")
	}
	// Three total runs: the two that blocked on release plus the one
	// we enqueued post-drain.
	wantAtLeast := int32(3)
	endByrun := time.Now().Add(time.Second)
	for ran.Load() < wantAtLeast && time.Now().Before(endByrun) {
		time.Sleep(5 * time.Millisecond)
	}
	if ran.Load() < wantAtLeast {
		t.Errorf("ran = %d, want >= %d", ran.Load(), wantAtLeast)
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
