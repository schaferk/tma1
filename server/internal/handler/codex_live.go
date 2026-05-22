package handler

import (
	"sync"
	"time"
)

// codexLiveSessionStore tracks which Codex sessions are actively
// posting events to /api/hooks. The transcript JSONL parser
// (server/internal/transcript/codex.go) consults this before
// inserting its own rows — when a session is "live" we want only
// one writer.
//
// Why a map instead of relying on session_id+ts dedup at the DB
// level: the hook posts with server-side time.Now() and the JSONL
// line carries Codex's own rollout timestamp, so the two never
// collide on the same `ts` value. Naive dedup on (session_id, ts,
// event_type) wouldn't work.
//
// codexLiveStaleAfter bounds how long after the last hook arrival
// we treat a session as live. Longer than the longest plausible
// JSONL-flush lag (a few seconds), short enough that a Codex
// session ending and a new one with the same prefix reusing the
// session_id wouldn't shadow the new one's transcript path.
const codexLiveStaleAfter = 30 * time.Second

// codexLiveGCInterval bounds how often we drop dead entries.
const codexLiveGCInterval = 5 * time.Minute

type codexLiveSessionStore struct {
	mu      sync.Mutex
	last    map[string]time.Time
	nextGC  time.Time
}

var codexLiveSessions = &codexLiveSessionStore{
	last: make(map[string]time.Time),
}

// markLive records that sessionID just emitted a hook event.
// Opportunistically GCs dead entries on a 5-min cadence so the map
// can't grow unbounded.
func (s *codexLiveSessionStore) markLive(sessionID string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[sessionID] = now
	if now.After(s.nextGC) {
		cutoff := now.Add(-codexLiveStaleAfter)
		for sid, ts := range s.last {
			if ts.Before(cutoff) {
				delete(s.last, sid)
			}
		}
		s.nextGC = now.Add(codexLiveGCInterval)
	}
}

// IsLive returns true when sessionID emitted a hook within the
// stale-after window. Used as the gate the transcript parser
// consults via Watcher.IsLiveSession.
func (s *codexLiveSessionStore) IsLive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.last[sessionID]
	if !ok {
		return false
	}
	return time.Since(last) < codexLiveStaleAfter
}

// IsCodexSessionLive is the package-level accessor wired into the
// transcript watcher at startup. Lives here so main.go has a stable
// symbol to install on tw.IsLiveSession without exposing the store
// type.
func IsCodexSessionLive(sessionID string) bool {
	return codexLiveSessions.IsLive(sessionID)
}
