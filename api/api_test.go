package api

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/sch"
)

func evolved() *Ontology {
	o := New(
		Column{Name: "x", Typ: sch.Int, Required: true},
		Column{Name: "y", Typ: sch.Str, Required: true},
		Column{Name: "m", Typ: sch.Str, Required: true},
	)
	o.AddColumn("z", "str", true)
	o.ChangeType("y", "int")
	o.ChangeType("x", "str")
	o.DropColumn("m")
	o.AddColumn("w", "int", false)
	return o
}

// res renders a Map result as a comparable string.
func res(got []any, err error) string {
	switch err {
	case nil:
		return fmt.Sprint(got)
	case sch.ErrBadValue:
		return "ErrBadValue"
	case sch.ErrMissingColumn:
		return "ErrMissingColumn"
	}
	return err.Error()
}

// TestSixEvents: the six events from NOTES.md mapped to active.
func TestSixEvents(t *testing.T) {
	o := evolved()
	cases := []struct {
		ver  int
		vals []any
		want string
	}{
		{6, []any{"1", int64(2), "a", int64(3)}, "[1 2 a 3]"},
		{2, []any{int64(5), "6", "M", "b"}, "[5 6 b 0]"},
		{2, []any{int64(5), "abc", "M", "b"}, "ErrBadValue"},
		{4, []any{"9", int64(10), "M", "d"}, "[9 10 d 0]"},
		{1, []any{int64(11), "12", "M"}, "ErrMissingColumn"},
		{3, []any{int64(13), int64(14), "M", "e"}, "[13 14 e 0]"},
	}
	for i, c := range cases {
		if got := res(o.Map(c.ver, c.vals)); got != c.want {
			t.Errorf("event %d: got %s, want %s", i+1, got, c.want)
		}
	}
}

// TestRejectLeavesState: every fault class is decidable, distinct, and
// leaves no trace; the instance stays usable afterwards.
func TestRejectLeavesState(t *testing.T) {
	o := evolved()
	before := fmt.Sprint(o.Active())
	bad := []error{
		o.AddColumn("q", "float", false), // invalid type
		o.AddColumn("", "int", false),    // empty name
		o.AddColumn("x", "int", false),   // duplicate
		o.DropColumn("nope"),             // unknown column
		o.ChangeType("nope", "int"),      // unknown column
	}
	want := []error{sch.ErrBadType, sch.ErrEmptyName, sch.ErrDuplicate,
		sch.ErrUnknownColumn, sch.ErrUnknownColumn}
	for i := range bad {
		if bad[i] != want[i] {
			t.Fatalf("op %d: %v, want %v", i, bad[i], want[i])
		}
	}
	vers := []int{99, 2, 1}
	vals := [][]any{nil, {int64(5), "abc", "M", "b"}, {int64(1), "2", "M"}}
	werrs := []error{sch.ErrUnknownVersion, sch.ErrBadValue, sch.ErrMissingColumn}
	for i := range vers {
		if _, err := o.Map(vers[i], vals[i]); err != werrs[i] {
			t.Fatalf("Map(%d): %v, want %v", vers[i], err, werrs[i])
		}
	}
	if fmt.Sprint(o.Active()) != before {
		t.Fatal("rejected ops changed state")
	}
	if _, err := o.Map(6, []any{"1", int64(2), "a", int64(3)}); err != nil {
		t.Fatal("unusable after rejections:", err)
	}
	if err := o.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentMap: one writer keeps bumping versions while readers Map
// the same v1 event; every result matches one complete schema, never a mix.
func TestConcurrentMap(t *testing.T) {
	o := New(Column{Name: "a", Typ: sch.Int, Required: true})
	var done atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // writer
		defer wg.Done()
		for i := 0; i < 200; i++ {
			o.AddColumn(fmt.Sprint("w", i), "int", false)
		}
		done.Store(1)
	}()
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for done.Load() == 0 {
				got, err := o.Map(1, []any{int64(7)})
				bad := err != nil || got[0] != int64(7)
				for _, v := range got[1:] { // appended optional cols are zero
					bad = bad || v != int64(0)
				}
				if bad {
					t.Errorf("mixed schema: %v, %v", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
