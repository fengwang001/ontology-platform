package pcol

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func vs(s string) Value { return Value{S: s} }
func vn() Value         { return Value{Null: true} }
func kg(cols ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, c := range cols {
		m[c] = struct{}{}
	}
	return m
}
func ps(s string) map[string]Value {
	m := map[string]Value{}
	for _, p := range strings.Split(s, ",") {
		if p == "" {
			continue
		}
		q := strings.SplitN(p, ":", 2)
		m[q[0]] = vs(q[1])
		if q[1] == "NULL" {
			m[q[0]] = vn()
		}
	}
	return m
}
func en(m map[string]Value) string {
	p := []string{}
	for c := 'a'; c <= 'z'; c++ {
		if v, ok := m[string(c)]; ok {
			s := v.S
			if v.Null {
				s = "NULL"
			}
			p = append(p, string(c)+"="+s)
		}
	}
	return strings.Join(p, ",")
}
func up(s, b string) Event { return Event{Kind: KindUpdate, Set: ps(s), Before: ps(b)} }
func TestValueEqual(t *testing.T) {
	for _, c := range []struct {
		x, y Value
		w    bool
	}{
		{vn(), vn(), true}, {vn(), vs(""), false}, {vs(""), vn(), false},
		{vs(""), vs(""), true}, {vs("a"), vs("a"), true}, {vs("a"), vs("b"), false},
	} {
		if c.x.Equal(c.y) != c.w {
			t.Fatalf("%+v.Equal(%+v)", c.x, c.y)
		}
	}
}
func TestValidate(t *testing.T) {
	for _, c := range []struct {
		e   Event
		bad bool
	}{
		{Event{Kind: KindInsert, Set: ps("a:1,b:NULL")}, false},
		{Event{Kind: KindInsert, Set: ps("a:1")}, true},
		{Event{Kind: KindInsert, Set: ps("a:1,b:NULL"), Before: ps("a:x")}, true},
		{Event{Kind: KindUpdate, Set: ps("a:2"), Before: ps("a:1")}, false},
		{Event{Kind: KindUpdate}, true},
		{Event{Kind: KindUpdate, Set: ps("a:2"), Before: ps("b:1")}, true},
		{Event{Kind: KindUpdate, Set: ps("z:2"), Before: ps("z:1")}, true},
		{Event{Set: ps("a:1,b:NULL")}, true},
	} {
		err := Validate(c.e, kg("a", "b"))
		if (err != nil) != c.bad || c.bad && !errors.Is(err, ErrBadColumn) {
			t.Fatalf("%+v: err=%v bad=%v", c.e, err, c.bad)
		}
	}
}
func TestMerge(t *testing.T) {
	steps := []string{
		"a:2|a:1|a=2|a=1",
		"b:NULL|b:x|a=2,b=NULL|a=1,b=x",
		"a:3,c:z|a:2,c:NULL|a=3,b=NULL,c=z|a=1,b=x,c=NULL",
		"b:x|b:NULL|a=3,c=z|a=1,c=NULL",
		"c:NULL|c:z|a=3|a=1",
		"a:NULL|a:3|a=NULL|a=1",
	}
	var mg *Merger
	for i, s := range steps {
		q := strings.SplitN(s, "|", 4)
		var err error
		if i == 0 {
			mg, err = NewMerger(up(q[0], q[1]), kg("a", "b", "c"))
		} else {
			err = mg.Merge(up(q[0], q[1]))
		}
		if err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		r, ok := mg.Result("k")
		if !ok || en(r.Set) != q[2] || en(r.Before) != q[3] {
			t.Fatalf("step %d: %s|%s want %s|%s", i+1, en(r.Set), en(r.Before), q[2], q[3])
		}
	}
	im, _ := NewMerger(Event{Kind: KindInsert, Set: ps("a:1,b:NULL")}, kg("a", "b"))
	if err := im.Merge(up("a:2", "a:1")); err != nil {
		t.Fatal(err)
	}
	if ir, ok := im.Result("k"); !ok || ir.Kind != KindInsert || en(ir.Set) != "a=2,b=NULL" {
		t.Fatalf("insert+update: %+v", ir)
	}
	zm, _ := NewMerger(up("a:2", "a:1"), kg("a"))
	if err := zm.Merge(up("a:1", "a:2")); err != nil {
		t.Fatal(err)
	}
	if _, ok := zm.Result("k"); ok {
		t.Fatal("zero-net-change key must not be emitted")
	}
	rm, _ := NewMerger(up("b:NULL", "b:x"), kg("b"))
	for _, s := range [][2]string{{"b:x", "b:NULL"}, {"b:y", "b:x"}} {
		if err := rm.Merge(up(s[0], s[1])); err != nil {
			t.Fatal(err)
		}
	}
	if rr, ok := rm.Result("k"); !ok || en(rr.Set) != "b=y" || en(rr.Before) != "b=x" {
		t.Fatalf("revived column: %s|%s", en(rr.Set), en(rr.Before))
	}
}

// TestMergeCheckedColumns 钉住第四节：第二次合并计数恒为 2，不随 m 增长。
func TestMergeCheckedColumns(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		cols, set, bef := map[string]struct{}{}, map[string]Value{}, map[string]Value{}
		for i := range m {
			c := "c" + strconv.Itoa(i)
			cols[c], set[c], bef[c] = struct{}{}, vs("n"), vs("o")
		}
		mg, err := NewMerger(Event{Kind: KindUpdate, Set: set, Before: bef}, cols)
		if err != nil {
			t.Fatal(err)
		}
		if err := mg.Merge(Event{Kind: KindUpdate, Set: ps("c0:z"), Before: ps("c0:n")}); err != nil {
			t.Fatal(err)
		}
		if mg.lastChecked > 1+7 || mg.lastChecked != 2 {
			t.Fatalf("m=%d checked %d columns, want 2 (independent of m)", m, mg.lastChecked)
		}
	}
}
