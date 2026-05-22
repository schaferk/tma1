package greptimedb

import (
	"fmt"
	"log/slog"
	"strings"
)

// buildTableDDL creates tma1_build_events.
//
// Wide-event schema for build/dev subprocess output captured by
// `tma1-server build -- <cmd>`. Append-only; SKIPPING INDEX on project for
// per-project queries, INVERTED INDEX on event_type for filtering errors,
// FULLTEXT on message so the dashboard can search by error text.
//
// `message`, `stream`, `tag` are GreptimeDB reserved keywords — quoted.
// buildTableDDL — project + event_type are the dominant filters
// (every GetBuildStatus query is scoped to one project's tag) and
// both are low-cardinality (a few projects × 3 event_types:
// started / output / completed). Make them PRIMARY KEY for locality.
// Keep INVERTED on severity (used as a filter); FULLTEXT on message
// for dashboard keyword search.
var buildTableDDL = `CREATE TABLE IF NOT EXISTS tma1_build_events (
    ts          TIMESTAMP TIME INDEX,
    project     STRING,
    event_type  STRING,
    command     STRING NULL,
    severity    STRING NULL INVERTED INDEX,
    "stream"    STRING NULL,
    "message"   STRING NULL FULLTEXT INDEX WITH (backend='bloom', analyzer='English', case_sensitive='false'),
    file_path   STRING NULL,
    line_no     INT NULL,
    exit_code   INT NULL,
    duration_ms BIGINT NULL,
    host        STRING NULL,
    "tag"       STRING NULL,
    PRIMARY KEY (project, event_type)
) WITH ('append_mode'='true')`

// InitBuildTable creates tma1_build_events. Idempotent.
// Kept separate from flows.sql per the plan — flows.sql is Flow/sink only;
// regular event tables get dedicated init functions.
func InitBuildTable(httpPort int, logger *slog.Logger) error {
	sqlURL := fmt.Sprintf("http://localhost:%d/v1/sql", httpPort)
	if err := execSQL(sqlURL, buildTableDDL); err != nil {
		// Tolerate "already exists" idempotently.
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("init build_events: %w", err)
		}
	}
	logger.Info("build_events table initialized")
	return nil
}
