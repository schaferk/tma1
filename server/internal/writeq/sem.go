// Package writeq provides a tiny bounded semaphore used to cap the number
// of concurrent background HTTP writes to GreptimeDB. Hook event inserts
// and anomaly emits both push fire-and-forget INSERTs from request paths
// — under burst (subagent storms, replay) the naive `go ...` pattern
// would accumulate thousands of in-flight goroutines, each holding an
// HTTP connection.
//
// Sem caps the inflight count and drops jobs that would exceed it,
// surfacing the drop count via Dropped() so callers can alert on it.
// Failure mode is data loss, not OOM — by design.
//
// Background fn() invocations run under a defer recover() so a panic in
// a single write turns into a logged drop instead of crashing the
// server. Without that, a misbehaving callback would take down hook
// ingest + anomaly emit + every other shared writer.
package writeq

import (
	"log/slog"
	"sync/atomic"
)

// Sem is a counting semaphore that spawns work in goroutines. Safe for
// concurrent use.
type Sem struct {
	ch       chan struct{}
	dropped  atomic.Uint64
	panicked atomic.Uint64
	logger   *slog.Logger
}

// New returns a Sem allowing up to maxInFlight concurrent jobs. Pass <= 0
// to use the default of 32. The returned Sem logs recovered panics via
// slog.Default(); callers wanting structured logging should construct
// via NewWithLogger.
func New(maxInFlight int) *Sem {
	return NewWithLogger(maxInFlight, nil)
}

// NewWithLogger returns a Sem like New but with an explicit logger
// used to record recovered panics in background fn() invocations.
// A nil logger falls back to slog.Default().
func NewWithLogger(maxInFlight int, logger *slog.Logger) *Sem {
	if maxInFlight <= 0 {
		maxInFlight = 32
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Sem{ch: make(chan struct{}, maxInFlight), logger: logger}
}

// Go runs fn in a new goroutine when there's capacity, returning true.
// When the in-flight limit is reached it drops the job and returns
// false; the caller is responsible for logging (Sem stays stateless on
// the logging side so it can be reused across packages with different
// loggers).
func (s *Sem) Go(fn func()) bool {
	if fn == nil {
		return false
	}
	select {
	case s.ch <- struct{}{}:
		go func() {
			// Slot release must run even on panic, otherwise a
			// single misbehaving fn() would starve the semaphore.
			defer func() { <-s.ch }()
			// Recover so a panic in fn() doesn't take down the
			// whole server. We log it (best-effort) and bump a
			// counter so the failure isn't silent.
			defer func() {
				if r := recover(); r != nil {
					s.panicked.Add(1)
					if s.logger != nil {
						s.logger.Error("writeq: recovered panic in background fn",
							"panic", r,
							"panicked_total", s.panicked.Load())
					}
				}
			}()
			fn()
		}()
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

// Panicked returns the cumulative number of background fn() invocations
// that crashed and were recovered. A non-zero value is the signal that
// something downstream is throwing -- worth investigating, never normal.
func (s *Sem) Panicked() uint64 {
	return s.panicked.Load()
}

// Dropped returns the cumulative number of jobs Go has refused due to
// the in-flight cap.
func (s *Sem) Dropped() uint64 {
	return s.dropped.Load()
}
