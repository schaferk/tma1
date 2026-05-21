package project

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tma1-ai/tma1/server/internal/pathutil"
)

// IndexTTL — how long an indexed snapshot is considered "fresh". When
// Index(cwd) is called for the same project within this window we skip the
// re-scan + DB write. Subsequent calls past the TTL re-index so the agent
// sees updated structure (new key files, new top-level dirs, etc.) without
// a server restart.
const IndexTTL = 24 * time.Hour

// Sensor is the long-lived owner of project-state indexing. Call Index(cwd)
// from the hook handler on every event — Sensor itself dedupes by project
// root and TTL.
type Sensor struct {
	writer EventWriter
	logger *slog.Logger

	mu     sync.Mutex
	lastAt map[string]time.Time // project_root → last-indexed time
}

// NewSensor returns a Sensor.
func NewSensor(writer EventWriter, logger *slog.Logger) *Sensor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sensor{
		writer: writer,
		logger: logger,
		lastAt: map[string]time.Time{},
	}
}

// Index resolves cwd to a project root and writes a fresh State row if the
// project hasn't been indexed within IndexTTL. Idempotent + cheap; safe to
// call on every hook event. The write itself runs in a goroutine so the
// caller (handler) is never blocked.
func (s *Sensor) Index(cwd string) {
	root := resolveProjectRoot(cwd)
	if root == "" {
		return
	}
	claimedAt, ok := s.claimSlot(root)
	if !ok {
		return
	}
	go s.indexAndWrite(context.Background(), root, claimedAt)
}

// IndexAndWait runs the index synchronously, bounded by timeout. SessionStart
// uses this path so the subsequent bundle query actually sees the just-
// written project_state row — the fire-and-forget Index races against the
// bundler's SELECT and returns stale or empty state on a cold session.
//
// On TTL-skip it returns nil immediately. On error or timeout it logs and
// returns nil — caller continues with stale / missing state rather than
// blocking the hook response on a slow GreptimeDB.
func (s *Sensor) IndexAndWait(cwd string, timeout time.Duration) {
	root := resolveProjectRoot(cwd)
	if root == "" {
		return
	}
	claimedAt, ok := s.claimSlot(root)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.indexAndWrite(ctx, root, claimedAt)
}

// claimSlot returns the reservation timestamp and true when the caller should
// index, or false when the TTL gate is still in effect.
//
// The timestamp is stored before releasing the lock so concurrent callers
// don't double-index the same project. If the write fails, indexAndWrite
// clears that exact reservation so the next hook can retry instead of being
// suppressed for IndexTTL.
func (s *Sensor) claimSlot(root string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAt[root]; ok && time.Since(last) < IndexTTL {
		return time.Time{}, false
	}
	now := time.Now()
	s.lastAt[root] = now
	return now, true
}

// clearClaim deletes a failed reservation if no newer caller has claimed the
// same root since. This keeps a timeout/failure from poisoning the TTL gate.
func (s *Sensor) clearClaim(root string, claimedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAt[root]; ok && last.Equal(claimedAt) {
		delete(s.lastAt, root)
	}
}

func (s *Sensor) indexAndWrite(ctx context.Context, root string, claimedAt time.Time) {
	state := Index(projectLabel(root), root)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	if err := s.writer.Write(ctx, state); err != nil {
		s.clearClaim(root, claimedAt)
		s.logger.Debug("project sensor: write failed", "err", err, "project", state.Project)
	}
}

// resolveProjectRoot duplicates perception's helper so this package has no
// dependency on perception (which will eventually read project_state back,
// creating a cycle otherwise).
func resolveProjectRoot(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	if r := findAncestorWith(abs, ".git"); r != "" {
		return r
	}
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "pom.xml"}
	dir := abs
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return abs
}

func findAncestorWith(start, marker string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func projectLabel(root string) string {
	return pathutil.Basename(strings.TrimRight(root, `/\`))
}
