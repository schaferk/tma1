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
package writeq

import "sync/atomic"

// Sem is a counting semaphore that spawns work in goroutines. Safe for
// concurrent use.
type Sem struct {
	ch      chan struct{}
	dropped atomic.Uint64
}

// New returns a Sem allowing up to maxInFlight concurrent jobs. Pass <= 0
// to use the default of 32.
func New(maxInFlight int) *Sem {
	if maxInFlight <= 0 {
		maxInFlight = 32
	}
	return &Sem{ch: make(chan struct{}, maxInFlight)}
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
			defer func() { <-s.ch }()
			fn()
		}()
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

// Dropped returns the cumulative number of jobs Go has refused due to
// the in-flight cap.
func (s *Sem) Dropped() uint64 {
	return s.dropped.Load()
}
