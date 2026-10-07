package aggview

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"
)

// memLogger 收集并按需打印每次变更的输入、受影响分组与判定依据。
type memLogger struct {
	events []Event
	t      *testing.T
}

func (l *memLogger) Log(ev Event) {
	l.events = append(l.events, ev)
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] input={%s}", ev.Op, ev.Input)
	if ev.Reason != "" {
		fmt.Fprintf(&b, " reason={%s}", strings.TrimSpace(ev.Reason))
	}
	for _, a := range ev.Affected {
		fmt.Fprintf(&b, " view=%s/group=%s delta=%s dCount=%d",
			a.View, a.Group, a.Delta.RatString(), a.DCount)
	}
	if ev.Err != "" {
		fmt.Fprintf(&b, " ERROR=%s", ev.Err)
	}
	if l.t != nil {
		l.t.Log(b.String())
	}
}

func newTestEngine(t *testing.T) (*Engine, *Store, *memLogger) {
	st := NewStore()
	l := &memLogger{t: t}
	return NewEngine(st, l), st, l
}

func mustCreate(t *testing.T, st *Store, id, typ ID) {
	t.Helper()
	if err := st.CreateObject(id, typ); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rat %q", s)
	}
	return r
}

func assertAgg(t *testing.T, e *Engine, view, group ID, wantSum string, wantCount int) {
	t.Helper()
	got := e.Query(view, group)
	want := mustRat(t, wantSum)
	if got.Sum.Cmp(want) != 0 || got.Count != wantCount {
		t.Fatalf("query view=%s group=%s = (%s, count=%d), want (%s, count=%d)",
			view, group, got.Sum.RatString(), got.Count, wantSum, wantCount)
	}
}

func assertKind(t *testing.T, err error, want Kind) {
	t.Helper()
	if ErrorKind(err) != want {
		t.Fatalf("error kind = %v (%v), want %v", ErrorKind(err), err, want)
	}
}

func sortedGroups(ts []Touched) string {
	cp := append([]Touched(nil), ts...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].View != cp[j].View {
			return cp[i].View < cp[j].View
		}
		return cp[i].Group < cp[j].Group
	})
	var parts []string
	for _, t2 := range cp {
		parts = append(parts, string(t2.View)+"/"+string(t2.Group))
	}
	return strings.Join(parts, ",")
}

func ratIface(n int64) Value { return FromInt(n) }
