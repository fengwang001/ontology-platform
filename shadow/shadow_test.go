package shadow

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/doc"
)

func upsertMap(e []DeltaEntry) map[string]any {
	m := map[string]any{}
	for _, x := range e {
		m[x.Path] = x.Value
	}
	return m
}

func mvOf(t *testing.T, s *Service, dev, side, path string) int {
	t.Helper()
	d := s.devs[dev]
	var root = d.desired
	if side == "reported" {
		root = d.reported
	}
	n := doc.Find(root, split(path))
	if n == nil || n.Leaf == nil {
		t.Fatalf("leaf %s not found in %s", path, side)
	}
	return n.Leaf.Meta
}

func split(p string) []string {
	var segs []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '.' {
			segs = append(segs, p[start:i])
			start = i + 1
		}
	}
	return append(segs, p[start:])
}

func TestSpecExample(t *testing.T) {
	s := NewService(10000)
	if err := s.Create("dev"); err != nil {
		t.Fatal(err)
	}
	r1, err := s.UpdateDesired("dev", map[string]any{
		"a": map[string]any{"b": int64(1), "c": int64(2)}, "d": "x",
	}, 0)
	if err != nil || r1.Ver != 1 {
		t.Fatalf("step1: %v ver=%v", err, r1)
	}
	if got := upsertMap(r1.Change.Upsert); fmt.Sprint(got) != "map[a.b:1 a.c:2 d:x]" {
		t.Fatalf("step1 upsert = %v", got)
	}
	if len(r1.Change.Remove) != 0 {
		t.Fatalf("step1 remove = %v", r1.Change.Remove)
	}
	for _, p := range []string{"a.b", "a.c", "d"} {
		if mvOf(t, s, "dev", "desired", p) != 1 {
			t.Fatalf("step1 mv %s != 1", p)
		}
	}

	r2, err := s.UpdateReported("dev", map[string]any{"a": map[string]any{"b": int64(1)}, "d": "y"}, 0)
	if err != nil || r2.Ver != 2 {
		t.Fatalf("step2: %v", err)
	}
	if len(r2.Change.Upsert) != 0 || fmt.Sprint(r2.Change.Remove) != "[a.b]" {
		t.Fatalf("step2 change = %+v", r2.Change)
	}

	r3, err := s.UpdateDesired("dev", map[string]any{"a": map[string]any{"c": nil}, "d": "x"}, 0)
	if err != nil || r3.Ver != 3 {
		t.Fatalf("step3: %v", err)
	}
	if fmt.Sprint(r3.Change.Remove) != "[a.c]" || len(r3.Change.Upsert) != 0 {
		t.Fatalf("step3 change = %+v", r3.Change)
	}
	if mvOf(t, s, "dev", "desired", "d") != 1 {
		t.Fatalf("d mv changed on no-op write")
	}

	r4, err := s.UpdateDesired("dev", map[string]any{"a": map[string]any{"b": map[string]any{}}}, 0)
	if err != nil || r4.Ver != 4 {
		t.Fatalf("step4: %v", err)
	}
	if len(r4.Change.Upsert) != 0 || len(r4.Change.Remove) != 0 {
		t.Fatalf("step4 change = %+v", r4.Change)
	}
	all, _ := s.GetDelta("dev")
	if got := upsertMap(all); fmt.Sprint(got) != "map[d:x]" {
		t.Fatalf("after step4 delta = %v", got)
	}

	r5, err := s.UpdateDesired("dev", map[string]any{"e": map[string]any{}}, 0)
	if err != nil || r5.Ver != 4 {
		t.Fatalf("step5: %v ver=%d", err, r5.Ver)
	}
	if len(r5.Change.Upsert) != 0 || len(r5.Change.Remove) != 0 {
		t.Fatalf("step5 should be no change: %+v", r5.Change)
	}

	if _, err := s.UpdateReported("dev", map[string]any{"d": "x"}, 3); !errors.Is(err, ErrVersion) {
		t.Fatalf("expect=3 want ErrVersion, got %v", err)
	}
	r6, err := s.UpdateReported("dev", map[string]any{"d": "x"}, 4)
	if err != nil || r6.Ver != 5 {
		t.Fatalf("step6: %v", err)
	}
	if fmt.Sprint(r6.Change.Remove) != "[d]" || len(r6.Change.Upsert) != 0 {
		t.Fatalf("step6 change = %+v", r6.Change)
	}
	all, _ = s.GetDelta("dev")
	if len(all) != 0 {
		t.Fatalf("final delta = %v", all)
	}
}

func TestObjectLeafSwap(t *testing.T) {
	s := NewService(10000)
	_ = s.Create("d")
	if _, err := s.UpdateDesired("d", map[string]any{"m": map[string]any{"x": int64(1), "y": int64(2)}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateReported("d", map[string]any{"m": int64(5)}, 0); err != nil {
		t.Fatal(err)
	}
	got := upsertMap(mustDelta(t, s, "d"))
	if fmt.Sprint(got) != "map[m.x:1 m.y:2]" {
		t.Fatalf("delta under object/leaf mismatch = %v", got)
	}
	if _, err := s.UpdateReported("d", map[string]any{"m": map[string]any{"x": int64(1)}}, 0); err != nil {
		t.Fatal(err)
	}
	got = upsertMap(mustDelta(t, s, "d"))
	if fmt.Sprint(got) != "map[m.y:2]" {
		t.Fatalf("delta after replacing leaf with object = %v", got)
	}

	// desired leaf vs reported object: the single desired leaf is in delta.
	s2 := NewService(10000)
	_ = s2.Create("d2")
	if _, err := s2.UpdateDesired("d2", map[string]any{"m": int64(7)}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.UpdateReported("d2", map[string]any{"m": map[string]any{"x": int64(1)}}, 0); err != nil {
		t.Fatal(err)
	}
	got = upsertMap(mustDelta(t, s2, "d2"))
	if fmt.Sprint(got) != "map[m:7]" {
		t.Fatalf("leaf/object mismatch delta = %v", got)
	}
}

func mustDelta(t *testing.T, s *Service, dev string) []DeltaEntry {
	t.Helper()
	e, err := s.GetDelta(dev)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCreateErrors(t *testing.T) {
	s := NewService(10)
	if err := s.Create(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty dev: %v", err)
	}
	if err := s.Create("x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("x"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, err := s.UpdateDesired("y", map[string]any{"a": int64(1)}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}
	if _, err := s.GetDelta("y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDelta missing: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	s := NewService(1)
	_ = s.Create("d")
	// invalid > missing
	if _, err := s.UpdateDesired("missing", map[string]any{}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty patch on missing dev: %v", err)
	}
	// invalid > version
	if _, err := s.UpdateDesired("d", map[string]any{"a.b": int64(1)}, 99); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid key must beat version: %v", err)
	}
	// version > too large
	_, err := s.UpdateDesired("d", map[string]any{"a": int64(1), "b": int64(2)}, 99)
	if !errors.Is(err, ErrVersion) {
		t.Fatalf("version must beat too large: %v", err)
	}
	// too large with correct expect
	if _, err := s.UpdateDesired("d", map[string]any{"a": int64(1), "b": int64(2)}, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	// exactly L accepted
	if _, err := s.UpdateDesired("d", map[string]any{"a": int64(1)}, 0); err != nil {
		t.Fatalf("exactly L: %v", err)
	}
	if _, err := s.UpdateDesired("d", map[string]any{"b": int64(2)}, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("L+1: %v", err)
	}
	// rejected updates changed nothing
	v, _ := s.GetVer("d")
	if v != 1 {
		t.Fatalf("ver after rejects = %d", v)
	}
}

func TestNoChangeKeepsVersion(t *testing.T) {
	s := NewService(100)
	_ = s.Create("d")
	_, _ = s.UpdateDesired("d", map[string]any{"a": int64(1), "b": map[string]any{"c": "z"}}, 0)
	r, err := s.UpdateDesired("d", map[string]any{"a": int64(1), "b": map[string]any{"c": "z"}, "x": nil}, 0)
	if err != nil || r.Ver != 1 {
		t.Fatalf("no-change update: %v ver=%d", err, r.Ver)
	}
	if len(r.Change.Upsert) != 0 || len(r.Change.Remove) != 0 {
		t.Fatalf("no-change delta: %+v", r.Change)
	}
}

func TestConcurrentSameExpect(t *testing.T) {
	s := NewService(10000)
	_ = s.Create("d")
	_, _ = s.UpdateDesired("d", map[string]any{"base": int64(0)}, 0)
	var wg sync.WaitGroup
	var inc, reject, nochange int64
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.UpdateDesired("d", map[string]any{"k": int64(i)}, 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrVersion):
				reject++
			case err != nil:
				t.Errorf("unexpected err %v", err)
			case r.Ver == 1:
				nochange++
			default:
				inc++
			}
		}(i)
	}
	wg.Wait()
	if inc != 1 || reject != 49 {
		t.Fatalf("inc=%d reject=%d nochange=%d", inc, reject, nochange)
	}
	v, _ := s.GetVer("d")
	if v != 2 {
		t.Fatalf("ver = %d", v)
	}
}

func TestReplayDeterminism(t *testing.T) {
	type op struct {
		side   bool
		patch  map[string]any
		expect int
	}
	mkOps := func() []op {
		return []op{
			{true, map[string]any{"a": map[string]any{"b": int64(1), "c": int64(2)}, "d": "x"}, 0},
			{false, map[string]any{"a": map[string]any{"b": int64(1)}, "d": "y"}, 0},
			{true, map[string]any{"a": map[string]any{"c": nil}, "d": "x"}, 0},
			{true, map[string]any{"a": map[string]any{"b": map[string]any{}}}, 0},
			{true, map[string]any{"e": map[string]any{}}, 0},
			{false, map[string]any{"d": "x"}, 4},
			{true, map[string]any{"a": int64(7)}, 0},
			{false, map[string]any{"a": map[string]any{"z": int64(3)}}, 0},
			{true, map[string]any{"a": nil}, 0},
		}
	}
	run := func() (vers []int, delta [][]DeltaEntry) {
		s := NewService(100)
		_ = s.Create("d")
		for _, o := range mkOps() {
			var (
				r   *Result
				err error
			)
			if o.side {
				r, err = s.UpdateDesired("d", o.patch, o.expect)
			} else {
				r, err = s.UpdateReported("d", o.patch, o.expect)
			}
			if err != nil {
				t.Fatal(err)
			}
			vers = append(vers, r.Ver)
			e, _ := s.GetDelta("d")
			delta = append(delta, e)
		}
		return vers, delta
	}
	v1, d1 := run()
	v2, d2 := run()
	if fmt.Sprint(v1) != fmt.Sprint(v2) {
		t.Fatalf("version replay differs: %v vs %v", v1, v2)
	}
	for i := range d1 {
		if fmt.Sprintf("%#v", d1[i]) != fmt.Sprintf("%#v", d2[i]) {
			t.Fatalf("delta replay differs at step %d:\n%#v\n%#v", i, d1[i], d2[i])
		}
	}
}
