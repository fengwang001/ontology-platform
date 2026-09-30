package merge

import (
	"fmt"
	"strings"
	"testing"
)

type captureLogger struct{ lines []string }

func (c *captureLogger) Logf(format string, args ...any) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

// The engine must log each step's input, the merged result, and the rationale.
func TestLoggingCapturesStepsInputsAndRationale(t *testing.T) {
	cl := &captureLogger{}
	tbl := NewTable(testSchema, cl)
	if _, err := tbl.Commit([]Event{ins("k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})}); err != nil {
		t.Fatal(err)
	}

	cl.lines = nil
	if _, err := tbl.Commit([]Event{
		upd("k", Row{"a": Str("A2")}, Row{"a": Str("A")}),
		upd("k", Row{"a": Str("A")}, Row{"a": Str("A2")}), // net no-op => pruned => dropped
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cl.lines, "\n")
	for _, want := range []string{"event[0]", "event[1]", "columns=", "before=", "drop", "merge: done"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("log missing %q:\n%s", want, joined)
		}
	}
}

// Error kinds must render as distinct, distinguishable strings.
func TestErrorKindsRenderDistinctly(t *testing.T) {
	kinds := []ErrorKind{ErrIllegalColumn, ErrKeyNotFound, ErrKeyExists, ErrBeforeImageMismatch, ErrConflict}
	seen := map[string]bool{}
	for _, k := range kinds {
		s := k.String()
		if seen[s] {
			t.Fatalf("duplicate kind string %q", s)
		}
		seen[s] = true
		e := &BatchError{Kind: k, Key: "k", Column: "c", EventIx: 0, Detail: "d"}
		if !strings.Contains(e.Error(), s) {
			t.Fatalf("Error() %q missing kind %q", e.Error(), s)
		}
	}
	if EventInsert.String() == EventUpdate.String() {
		t.Fatal("event kind strings must differ")
	}
}

func TestSchemaAccessorAndLoggerFunc(t *testing.T) {
	var got []string
	lf := LoggerFunc(func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) })
	tbl := NewTable(testSchema, lf)
	if !tbl.Schema().EqualSet(NewColumnSet(testSchema...)) {
		t.Fatal("schema mismatch")
	}
	if _, err := tbl.Commit([]Event{ins("k", Row{"a": Str("1"), "b": Str("2"), "c": Str("3")})}); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("LoggerFunc should receive trace lines")
	}
}
