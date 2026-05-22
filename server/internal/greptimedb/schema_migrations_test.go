package greptimedb

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestPendingMigrationsReturnsOnlyForwardEntries(t *testing.T) {
	all := []Migration{
		{Version: 1, Description: "first"},
		{Version: 2, Description: "second"},
		{Version: 3, Description: "third"},
	}
	cases := []struct {
		current int
		want    []int
	}{
		{0, []int{1, 2, 3}},
		{1, []int{2, 3}},
		{2, []int{3}},
		{3, nil},
		// Skip-ahead case (DB recorded a future version — e.g. dev
		// install was upgraded then rolled back). Don't replay v3.
		{99, nil},
	}
	for _, c := range cases {
		got := pendingMigrations(c.current, all)
		gotVers := make([]int, len(got))
		for i, m := range got {
			gotVers[i] = m.Version
		}
		if !equalIntSlice(gotVers, c.want) {
			t.Errorf("pendingMigrations(current=%d) = %v, want %v", c.current, gotVers, c.want)
		}
	}
}

func TestSchemaMigrationsCanonicalOrderIsStrictlyIncreasing(t *testing.T) {
	// Reordering or duplicating Version numbers silently breaks
	// production installs (the ledger keys off Version). Catch it at
	// build time.
	if len(schemaMigrations) == 0 {
		t.Fatal("schemaMigrations should not be empty — at least v1 ships")
	}
	versions := make([]int, len(schemaMigrations))
	for i, m := range schemaMigrations {
		versions[i] = m.Version
	}
	sorted := append([]int(nil), versions...)
	sort.Ints(sorted)
	if !equalIntSlice(versions, sorted) {
		t.Errorf("schemaMigrations not in ascending Version order: %v", versions)
	}
	seen := map[int]bool{}
	for _, v := range versions {
		if seen[v] {
			t.Errorf("duplicate migration Version %d", v)
		}
		seen[v] = true
	}
	if versions[0] != 1 {
		t.Errorf("first Version = %d, want 1", versions[0])
	}
}

func TestSchemaMigrationsCarrySQLAndIgnoreErr(t *testing.T) {
	// Each shipping migration must have at least one SQL statement and
	// an IgnoreErr that recognises the GreptimeDB "duplicate column"
	// surface — without it, the first install of the binary on a DB
	// that already has the v1/v2 columns would fail.
	for _, m := range schemaMigrations {
		if len(m.SQL) == 0 {
			t.Errorf("migration v%d has no SQL", m.Version)
		}
		if m.IgnoreErr == nil {
			t.Errorf("migration v%d has nil IgnoreErr — would crash on duplicate-column", m.Version)
			continue
		}
		if !m.IgnoreErr(errors.New("column already exists")) {
			t.Errorf("migration v%d IgnoreErr does not swallow already-exists", m.Version)
		}
		if m.IgnoreErr(errors.New("syntax error near 'foo'")) {
			t.Errorf("migration v%d IgnoreErr swallows real errors", m.Version)
		}
	}
}

// TestSchemaMigrationsHasNoDestructiveDDL is the data-safety guard:
// shipping migrations must NEVER `DROP TABLE`, `DROP COLUMN`,
// `TRUNCATE` an existing tma1_* table, or `RENAME` one. An early
// v3 draft used DROP+CREATE to retrofit a PRIMARY KEY and wiped
// every dogfood install's anomaly_emits / build_events /
// external_changes / project_state history before we caught it.
//
// If you genuinely need to reshape an existing table, do it via a
// non-destructive CREATE-NEW → INSERT…SELECT → swap path (and
// update this test's allowlist with a comment explaining why).
func TestSchemaMigrationsHasNoDestructiveDDL(t *testing.T) {
	destructive := []string{
		"DROP TABLE",
		"DROP COLUMN",
		"TRUNCATE",
		"RENAME TABLE",
		"ALTER TABLE.*RENAME",
	}
	for _, m := range schemaMigrations {
		for _, stmt := range m.SQL {
			upper := strings.ToUpper(stmt)
			for _, bad := range destructive {
				if strings.Contains(upper, bad) {
					t.Errorf("migration v%d contains destructive DDL %q: %s",
						m.Version, bad, stmt)
				}
			}
		}
	}
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
