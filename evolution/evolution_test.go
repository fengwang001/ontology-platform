package evolution

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// logProjection prints the event, the target schema and the per-column
// decision (or the rejection) plus the rule that justified it.
func logProjection(t *testing.T, label string, ev Event, target *Snapshot, report *ProjectionReport, err error) {
	t.Helper()
	t.Logf("==== %s ====", label)
	t.Logf("event(version=%d) columns:", ev.Version)
	for _, k := range sortedKeys(ev.Columns) {
		t.Logf("  event column %-8s = %v (%T)", k, ev.Columns[k], ev.Columns[k])
	}
	if target != nil {
		t.Logf("target schema version=%d:", target.schema.Version)
		for _, c := range target.schema.Columns {
			req := "optional"
			if c.Required {
				req = "required"
			}
			t.Logf("  target column %-8s type=%-6s %s", c.Name, c.Type, req)
		}
	}
	if report != nil {
		for _, r := range report.Results {
			t.Logf("  [%s] %-8s %s->%s in=%v out=%v | basis: %s",
				r.Decision, r.Name, r.SourceType, r.TargetType, r.SourceValue, r.OutValue, r.Basis)
		}
	}
	if err != nil {
		t.Logf("REJECT reason=%s err=%v", reasonOf(err), err)
	} else {
		t.Logf("ACCEPT")
	}
}

func reasonOf(err error) RejectReason {
	if re, ok := err.(*RejectError); ok {
		return re.Reason
	}
	return ""
}

func mustEvolve(t *testing.T, r *Registry, v int, ops ...ChangeOp) *Snapshot {
	t.Helper()
	snap, err := r.Evolve(v, ops)
	if err != nil {
		t.Fatalf("Evolve v%d failed: %v", v, err)
	}
	return snap
}

// TestSchemaValidation covers illegal type / empty name / duplicate name.
func TestSchemaValidation(t *testing.T) {
	r := NewRegistry()

	_, err := r.RegisterInitial(1, []ColumnDef{{Name: "a", Type: ColumnType("bool")}})
	if reasonOf(err) != ReasonInvalidType {
		t.Fatalf("want invalid_type, got %v", err)
	}
	t.Logf("illegal type rejected: %v", err)

	_, err = r.RegisterInitial(1, []ColumnDef{{Name: "", Type: TypeInt}})
	if reasonOf(err) != ReasonEmptyColumnName {
		t.Fatalf("want empty_column_name, got %v", err)
	}
	t.Logf("empty name rejected: %v", err)

	_, err = r.RegisterInitial(1, []ColumnDef{
		{Name: "a", Type: TypeInt},
		{Name: "a", Type: TypeString},
	})
	if reasonOf(err) != ReasonDuplicateColumn {
		t.Fatalf("want duplicate_column, got %v", err)
	}
	t.Logf("duplicate name rejected: %v", err)

	if r.Latest() != nil {
		t.Fatal("failed registration must not publish anything")
	}
}

// TestEvolveAddUpdateDelete covers add/update/delete and batch atomicity.
func TestEvolveAddUpdateDelete(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterInitial(1, []ColumnDef{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "name", Type: TypeString},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := r.Evolve(2, []ChangeOp{{Op: OpAdd, Column: ColumnDef{Name: "id", Type: TypeInt}}})
	if reasonOf(err) != ReasonColumnExists {
		t.Fatalf("want column_exists, got %v", err)
	}
	t.Logf("add existing rejected: %v", err)

	_, err = r.Evolve(2, []ChangeOp{{Op: OpUpdate, Column: ColumnDef{Name: "ghost", Type: TypeInt}}})
	if reasonOf(err) != ReasonUnknownColumn {
		t.Fatalf("want unknown_column, got %v", err)
	}
	t.Logf("update missing rejected: %v", err)

	_, err = r.Evolve(2, []ChangeOp{{Op: OpDelete, Column: ColumnDef{Name: "ghost"}}})
	if reasonOf(err) != ReasonUnknownColumn {
		t.Fatalf("want unknown_column, got %v", err)
	}
	t.Logf("delete missing rejected: %v", err)

	_, err = r.Evolve(2, []ChangeOp{{Op: "rename", Column: ColumnDef{Name: "id"}}})
	if reasonOf(err) != ReasonInvalidChangeOp {
		t.Fatalf("want invalid_change_op, got %v", err)
	}
	t.Logf("invalid op rejected: %v", err)

	_, err = r.Evolve(1, []ChangeOp{{Op: OpAdd, Column: ColumnDef{Name: "x", Type: TypeInt}}})
	if reasonOf(err) != ReasonVersionExists {
		t.Fatalf("want version_exists, got %v", err)
	}
	t.Logf("duplicate version rejected: %v", err)

	snap2 := mustEvolve(t, r, 2,
		ChangeOp{Op: OpAdd, Column: ColumnDef{Name: "tag", Type: TypeString}},
		ChangeOp{Op: OpUpdate, Column: ColumnDef{Name: "name", Type: TypeString, Required: true}},
	)
	if len(snap2.Schema().Columns) != 3 {
		t.Fatalf("want 3 columns, got %d", len(snap2.Schema().Columns))
	}

	// Mixed batch with one failing op must reject all and publish nothing.
	_, err = r.Evolve(3, []ChangeOp{
		{Op: OpDelete, Column: ColumnDef{Name: "tag"}},
		{Op: OpAdd, Column: ColumnDef{Name: "id", Type: TypeInt}},
	})
	if reasonOf(err) != ReasonColumnExists {
		t.Fatalf("want column_exists, got %v", err)
	}
	if _, err := r.Snapshot(3); reasonOf(err) != ReasonVersionNotRegistered {
		t.Fatalf("failed Evolve must not publish v3, got %v", err)
	}
	if r.Latest().Schema().Version != 2 {
		t.Fatal("failed batch must leave registry at v2")
	}
	t.Logf("mixed batch rejected atomically; latest stays v2: %v", err)

	snap4 := mustEvolve(t, r, 4, ChangeOp{Op: OpDelete, Column: ColumnDef{Name: "tag"}})
	if _, ok := snap4.column("tag"); ok {
		t.Fatal("tag should be deleted")
	}
	if _, ok := snap4.column("id"); !ok {
		t.Fatal("id must survive")
	}
}

// TestProjectByNameAlignment aligns by name, never by position, and drops
// deleted columns silently.
func TestProjectByNameAlignment(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterInitial(1, []ColumnDef{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "label", Type: TypeString},
		{Name: "legacy", Type: TypeString},
	}); err != nil {
		t.Fatal(err)
	}
	mustEvolve(t, r, 2,
		ChangeOp{Op: OpDelete, Column: ColumnDef{Name: "legacy"}},
		ChangeOp{Op: OpAdd, Column: ColumnDef{Name: "score", Type: TypeInt}},
	)

	// Keys given in scrambled order on purpose: position must not matter.
	ev := Event{Version: 1, Columns: map[string]any{
		"legacy": "old-data",
		"label":  "widget",
		"id":     int64(7),
	}}
	row, report, target, err := r.ProjectEvent(ev)
	logProjection(t, "name alignment + dropped column", ev, target, report, err)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	if row.Columns["id"] != int64(7) {
		t.Fatalf("id must align by name, got %v", row.Columns["id"])
	}
	if row.Columns["label"] != "widget" {
		t.Fatalf("label mismatch: %v", row.Columns["label"])
	}
	if row.Columns["score"] != int64(0) {
		t.Fatalf("missing optional column must zero-fill, got %v", row.Columns["score"])
	}
	if _, present := row.Columns["legacy"]; present {
		t.Fatal("deleted column must be silently dropped")
	}
	dropped := 0
	for _, cr := range report.Results {
		if cr.Decision == DecisionDropped && cr.Name == "legacy" {
			dropped++
		}
	}
	if dropped != 1 {
		t.Fatal("report must record exactly one dropped legacy column")
	}
}

// TestConversions covers int->string (always) and string->int (decidable).
func TestConversions(t *testing.T) {
	snap := newSnapshot(3, []ColumnDef{
		{Name: "numAsText", Type: TypeString},
		{Name: "textAsNum", Type: TypeInt},
		{Name: "plainNum", Type: TypeInt},
		{Name: "plainText", Type: TypeString},
	})

	bad := Event{Version: 3, Columns: map[string]any{
		"numAsText": int64(42),
		"textAsNum": "  ",
		"plainNum":  int64(5),
		"plainText": "hi",
	}}
	_, report, err := snap.Project(bad)
	logProjection(t, "string->int failure rejects whole event", bad, snap, report, err)
	if reasonOf(err) != ReasonConversionFailed {
		t.Fatalf("want conversion_failed, got %v", err)
	}

	good := Event{Version: 3, Columns: map[string]any{
		"numAsText": int64(-17),
		"textAsNum": "123",
		"plainNum":  int64(5),
		"plainText": "hi",
	}}
	row, report, err := snap.Project(good)
	logProjection(t, "conversions succeed", good, snap, report, err)
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if row.Columns["numAsText"] != "-17" {
		t.Fatalf("int->string failed: %v", row.Columns["numAsText"])
	}
	if row.Columns["textAsNum"] != int64(123) {
		t.Fatalf("string->int failed: %v", row.Columns["textAsNum"])
	}

	for _, lit := range []string{"12a", "", "3.14", "0x10", "+ 1"} {
		ev := Event{Version: 3, Columns: map[string]any{"textAsNum": lit}}
		_, rep, e := snap.Project(ev)
		if reasonOf(e) != ReasonConversionFailed {
			t.Fatalf("bad integer %q must reject, got %v", lit, e)
		}
		last := rep.Results[len(rep.Results)-1]
		t.Logf("bad integer %q rejected as expected: %v | basis: %s", lit, e, last.Basis)
	}
}

// TestRequiredMissing covers required rejection and optional zero fill.
func TestRequiredMissing(t *testing.T) {
	snap := newSnapshot(2, []ColumnDef{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "nick", Type: TypeString},
	})

	missing := Event{Version: 2, Columns: map[string]any{"nick": "bob"}}
	row, report, err := snap.Project(missing)
	logProjection(t, "required column missing", missing, snap, report, err)
	if reasonOf(err) != ReasonRequiredMissing || row != nil {
		t.Fatalf("want required_column_missing + nil row, got row=%v err=%v", row, err)
	}

	complete := Event{Version: 2, Columns: map[string]any{"id": int64(1)}}
	row, report, err = snap.Project(complete)
	logProjection(t, "optional missing -> zero value", complete, snap, report, err)
	if err != nil {
		t.Fatal(err)
	}
	if row.Columns["id"] != int64(1) || row.Columns["nick"] != "" {
		t.Fatalf("want id=1 nick=\"\", got %#v", row.Columns)
	}
}

// TestVersionNotRegistered covers queries/projection of unknown versions.
func TestVersionNotRegistered(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterInitial(1, []ColumnDef{{Name: "id", Type: TypeInt}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(99); reasonOf(err) != ReasonVersionNotRegistered {
		t.Fatalf("query unregistered version: %v", err)
	}
	ev := Event{Version: 404, Columns: map[string]any{"id": int64(1)}}
	_, _, _, err := r.ProjectEvent(ev)
	if reasonOf(err) != ReasonVersionNotRegistered {
		t.Fatalf("project unregistered event version: %v", err)
	}
	t.Logf("unregistered event version rejected: %v", err)

	empty := NewRegistry()
	_, err = empty.Evolve(1, nil)
	if reasonOf(err) != ReasonNoCurrentVersion {
		t.Fatalf("evolve before initial schema: %v", err)
	}
}

func renderRow(row *ProjectedRow) string {
	if row == nil {
		return "<nil>"
	}
	keys := make([]string, 0, len(row.Columns))
	for k := range row.Columns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%#v", k, row.Columns[k]))
	}
	return fmt.Sprintf("v%d{%s}", row.SchemaVersion, strings.Join(parts, ", "))
}

// TestDeterminism: same version + same value must always give the same row
// and the same per-column report, across repeated runs.
func TestDeterminism(t *testing.T) {
	snap := newSnapshot(1, []ColumnDef{
		{Name: "a", Type: TypeString},
		{Name: "b", Type: TypeInt},
	})
	ev := Event{Version: 1, Columns: map[string]any{
		"a":        int64(9),
		"gone":     "x",
		"alsoGone": "y",
	}}
	var first string
	var firstReport []ColumnResult
	for i := 0; i < 20; i++ {
		row, report, err := snap.Project(ev)
		if err != nil {
			t.Fatal(err)
		}
		got := renderRow(row)
		if i == 0 {
			first = got
			firstReport = report.Results
			continue
		}
		if got != first {
			t.Fatalf("non-deterministic row: %q vs %q", got, first)
		}
		if len(report.Results) != len(firstReport) {
			t.Fatal("report length differs across runs")
		}
		for j := range report.Results {
			if report.Results[j] != firstReport[j] {
				t.Fatalf("report[%d] differs: %+v vs %+v", j, report.Results[j], firstReport[j])
			}
		}
	}
	t.Logf("deterministic row over 20 runs: %s", first)
	// Manual column-by-column cross-check:
	// a: event int 9 -> target string => "9"; b: missing optional int => 0;
	// gone/alsoGone: not in target => dropped.
	row, report, err := snap.Project(ev)
	if err != nil {
		t.Fatal(err)
	}
	if row.Columns["a"] != "9" || row.Columns["b"] != int64(0) {
		t.Fatalf("manual cross-check failed: %#v", row.Columns)
	}
	var drops []string
	for _, cr := range report.Results {
		if cr.Decision == DecisionDropped {
			drops = append(drops, cr.Name)
		}
	}
	if strings.Join(drops, ",") != "alsoGone,gone" {
		t.Fatalf("dropped columns must be reported in sorted order, got %v", drops)
	}
	t.Logf("manual mapping check ok: a int64(9)->\"9\", b missing->int64(0), drops=%v", drops)
}

// TestConcurrentProjectionDuringEvolution hammers projections while the
// schema keeps evolving. Every accepted row must match one complete,
// registered version - never a mix of two versions.
func TestConcurrentProjectionDuringEvolution(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterInitial(1, []ColumnDef{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "c1", Type: TypeString},
	}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Evolver: keeps adding columns c2..cN.
	wg.Add(1)
	go func() {
		defer wg.Done()
		v := 2
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, err := r.Evolve(v, []ChangeOp{
				{Op: OpAdd, Column: ColumnDef{Name: fmt.Sprintf("c%d", v), Type: TypeString}},
			})
			if err != nil {
				t.Errorf("unexpected evolve failure: %v", err)
				return
			}
			v++
			if v > 40 {
				return
			}
		}
	}()

	// Projectors: each pins one snapshot and fully validates it.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev := Event{Version: 1, Columns: map[string]any{"id": int64(3), "c1": "seed"}}
			for i := 0; i < 200; i++ {
				snap, err := r.Snapshot(1) // query concurrently with evolution
				if err != nil {
					t.Errorf("snapshot lookup: %v", err)
					return
				}
				row, _, err := snap.Project(ev)
				if err != nil {
					t.Errorf("projection: %v", err)
					return
				}
				// Row must be complete for exactly the pinned schema.
				if len(row.Columns) != len(snap.schema.Columns) {
					t.Errorf("mixed-schema row: %d cols vs schema %d",
						len(row.Columns), len(snap.schema.Columns))
					return
				}
				for _, col := range snap.schema.Columns {
					v, ok := row.Columns[col.Name]
					if !ok {
						t.Errorf("row missing column %s of pinned schema v%d", col.Name, snap.schema.Version)
						return
					}
					switch col.Type {
					case TypeInt:
						if _, ok := v.(int64); !ok {
							t.Errorf("column %s not int64: %T", col.Name, v)
							return
						}
					case TypeString:
						if _, ok := v.(string); !ok {
							t.Errorf("column %s not string: %T", col.Name, v)
							return
						}
					}
				}
				if row.Columns["id"] != int64(3) {
					t.Errorf("id changed: %v", row.Columns["id"])
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)

	latest := r.Latest()
	t.Logf("concurrent run finished at schema version %d with %d columns",
		latest.Schema().Version, len(latest.Schema().Columns))
	if len(latest.Schema().Columns) != 41 {
		t.Fatalf("want 41 columns (id + c1 + c2..c40), got %d", len(latest.Schema().Columns))
	}
}
