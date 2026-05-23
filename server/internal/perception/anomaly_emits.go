package perception

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tma1-ai/tma1/server/internal/sqlutil"
)

// ListEmittedAnomalies returns anomalies recorded in tma1_anomaly_emits
// for sessionID over the last 24 hours, most-recent first. Side-effect-
// free: it issues a read-only SELECT and never advances the suppression
// window or writes to the emit log.
//
// This is the "past emitted history" path: callers wanting to show
// what has ALREADY been emitted to agents (dashboard `/api/anomalies`
// and any other history surface) should use this. The HTTP handler
// follows the same shape with its inline SELECT.
//
// NOT the right method for "current active anomalies" — MCP
// `get_anomalies` uses Detector.DetectPreview for that (it re-runs the
// rules + resolvers read-only). Don't conflate the two: ListEmitted
// returns history, DetectPreview returns the next-hook view.
//
// Empty sessionID returns nil. limit ≤ 0 defaults to 50; values above
// 500 are clamped (matches the dashboard handler's bounds).
func (d *Detector) ListEmittedAnomalies(ctx context.Context, sessionID string, limit int) ([]Anomaly, error) {
	if d == nil || d.client == nil {
		return nil, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	sql := fmt.Sprintf(
		`SELECT kind, severity, "channel", evidence, suggestion, related_files,
		        CAST(first_emitted_at AS BIGINT) AS first_ms
		 FROM tma1_anomaly_emits
		 WHERE session_id = '%s' AND ts > now() - INTERVAL '24 hours'
		 ORDER BY ts DESC LIMIT %d`,
		sqlutil.Escape(sessionID), limit,
	)
	_, rows, err := d.client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list emitted anomalies: %w", err)
	}

	out := make([]Anomaly, 0, len(rows))
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		a := Anomaly{
			Kind:       stringAt(r, 0),
			Severity:   stringAt(r, 1),
			Channel:    stringAt(r, 2),
			Evidence:   stringAt(r, 3),
			Suggestion: stringAt(r, 4),
		}
		if raw := stringAt(r, 5); raw != "" {
			_ = json.Unmarshal([]byte(raw), &a.RelatedFiles)
		}
		if firstMs := int64At(r, 6); firstMs > 0 {
			a.FirstEmittedAt = time.UnixMilli(firstMs)
		}
		out = append(out, a)
	}
	return out, nil
}

// logEmits writes one row per anomaly into tma1_anomaly_emits. Each row
// is an INSERT fired in its own goroutine — the hook's response budget
// is ~500ms and we don't want a slow DB to feed back into agent latency.
//
// Failures are intentionally swallowed: the emit log is dogfood
// infrastructure for the Phase 1.7 gates, not part of the agent's
// critical path. A missed row only thins the precision sample.
func (d *Detector) logEmits(sessionID string, anomalies []Anomaly) {
	if d == nil || d.client == nil || len(anomalies) == 0 {
		return
	}
	for _, a := range anomalies {
		a := a // capture
		fn := func() { d.insertEmit(sessionID, a) }
		if d.submit != nil {
			d.submit(fn)
		} else {
			go fn()
		}
	}
}

func (d *Detector) insertEmit(sessionID string, a Anomaly) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	related := ""
	if len(a.RelatedFiles) > 0 {
		if b, err := json.Marshal(a.RelatedFiles); err == nil {
			related = string(b)
		}
	}

	firstEmittedMs := int64(0)
	if !a.FirstEmittedAt.IsZero() {
		firstEmittedMs = a.FirstEmittedAt.UnixMilli()
	}

	sql := fmt.Sprintf(
		"INSERT INTO tma1_anomaly_emits "+
			"(ts, session_id, kind, severity, \"channel\", evidence, suggestion, related_files, first_emitted_at) "+
			"VALUES (%d, %s, %s, %s, %s, %s, %s, %s, %s)",
		time.Now().UnixMilli(),
		emitQuote(sessionID, 256),
		emitQuote(a.Kind, 64),
		emitQuote(a.Severity, 16),
		emitQuote(a.Channel, 32),
		emitQuote(a.Evidence, 1024),
		emitQuote(a.Suggestion, 1024),
		emitQuote(related, 2048),
		nullableMs(firstEmittedMs),
	)

	if _, _, err := d.client.Query(ctx, sql); err != nil {
		d.logger.Debug("anomaly emit log: insert failed", "err", err, "kind", a.Kind, "session", sessionID)
	}
}

// emitQuote: SQL literal with rune-safe truncation. Empty string
// becomes NULL so the column distinguishes "no value" from "empty
// string". Thin alias over sqlutil.Quote so the truncation policy
// stays consistent with sensor stores.
func emitQuote(v string, maxLen int) string { return sqlutil.Quote(v, maxLen) }

func nullableMs(ms int64) string {
	if ms <= 0 {
		return "NULL"
	}
	return fmt.Sprintf("%d", ms)
}
