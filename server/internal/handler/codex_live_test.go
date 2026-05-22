package handler

import (
	"testing"
	"time"
)

func TestCodexLiveSessionsBasic(t *testing.T) {
	s := &codexLiveSessionStore{last: map[string]time.Time{}}
	if s.IsLive("fresh") {
		t.Error("unseen session should not be live")
	}
	s.markLive("fresh")
	if !s.IsLive("fresh") {
		t.Error("just-marked session should be live")
	}
}

func TestCodexLiveSessionsExpiry(t *testing.T) {
	s := &codexLiveSessionStore{last: map[string]time.Time{}}
	// Pre-seed with an entry well past the stale window.
	s.last["old"] = time.Now().Add(-2 * codexLiveStaleAfter)
	if s.IsLive("old") {
		t.Error("session past stale-after should not be live")
	}
}

func TestCodexLiveSessionsEmptyIDNotLive(t *testing.T) {
	s := &codexLiveSessionStore{last: map[string]time.Time{}}
	if s.IsLive("") {
		t.Error("empty session ID should never be live")
	}
}

func TestCodexLiveSessionsGCsStaleEntries(t *testing.T) {
	s := &codexLiveSessionStore{last: map[string]time.Time{}}
	// Seed several dead entries + force the GC cadence into the past
	// so the next markLive() triggers the sweep.
	for i := 0; i < 8; i++ {
		s.last["dead"+string(rune('0'+i))] = time.Now().Add(-2 * codexLiveStaleAfter)
	}
	s.nextGC = time.Now().Add(-time.Second)
	s.markLive("fresh")
	// Fresh stays; dead entries are gone.
	if !s.IsLive("fresh") {
		t.Error("fresh entry should still be live after GC")
	}
	for i := 0; i < 8; i++ {
		key := "dead" + string(rune('0'+i))
		if _, ok := s.last[key]; ok {
			t.Errorf("dead entry %s should have been GC'd", key)
		}
	}
}
