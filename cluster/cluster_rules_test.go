package cluster

import (
	"errors"
	"fmt"
	"testing"
)

func ingest(t *testing.T, m *Merger, tenant, msg string) Result {
	t.Helper()
	got, err := m.Ingest(tenant, msg)
	if err != nil {
		t.Fatalf("Ingest(%q, %q): %v", tenant, msg, err)
	}
	t.Logf("input tenant=%q msg=%q output=%+v decision=scan leaf and select by eq, specificity, id", tenant, msg, got)
	return got
}

func TestNewValidation(t *testing.T) {
	cases := [][3]int{{0, 1, 1}, {101, 1, 1}, {1, 0, 1}, {1, 100001, 1}, {1, 1, 0}, {1, 1, 10001}}
	for _, in := range cases {
		if _, err := New(in[0], in[1], in[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) err=%v", in, err)
		}
	}
}

func TestSpecExampleAndMasking(t *testing.T) {
	m, _ := New(50, 10, 2)
	checks := []struct {
		msg  string
		id   int64
		new  bool
		text string
	}{
		{"open file a.txt ok", 1, true, "open file a.txt ok"},
		{"open file b.txt ok", 1, false, "open file <*> ok"},
		{"open dir c.txt fail", 1, false, "open <*> <*> <*>"},
		{"open 7 files ok", 1, false, "open <*> <*> <*>"},
		{"close file a.txt ok", 2, true, "close file a.txt ok"},
		{"123 x", 3, true, "<*> x"},
	}
	for _, want := range checks {
		got := ingest(t, m, "tenant", want.msg)
		if got.ID != want.id || got.Created != want.new {
			t.Fatalf("got %+v, want id=%d created=%v", got, want.id, want.new)
		}
		infos, _, _ := m.Templates("tenant")
		if infos[want.id-1].Text != want.text {
			t.Fatalf("template %d = %q, want %q", want.id, infos[want.id-1].Text, want.text)
		}
	}
}

func TestThresholdTieAndSpecificity(t *testing.T) {
	exact, _ := New(75, 10, 1)
	if r := ingest(t, exact, "t", "x a b c"); r.ID != 1 || !r.Created {
		t.Fatal("first template")
	}
	if r := ingest(t, exact, "t", "x a q c"); r.Created || r.ID != 1 {
		t.Fatalf("eq=3 at threshold should match existing id: %+v", r)
	}
	below, _ := New(76, 10, 1)
	ingest(t, below, "t", "x a b c")
	if r := ingest(t, below, "t", "x a q c"); r.ID != 2 || !r.Created {
		t.Fatalf("eq=3 below threshold must create: %+v", r)
	}

	tie, _ := New(75, 10, 1)
	for _, msg := range []string{"x a b c", "x p q c", "x a q c"} {
		ingest(t, tie, "t", msg)
	}
	infos, _, _ := tie.Templates("t")
	if infos[0].Text != "x a <*> c" || infos[1].Text != "x p q c" || infos[1].Count != 1 {
		t.Fatalf("tie result = %+v, expected more specific id=2", infos)
	}

	specific, _ := New(75, 10, 1)
	ingest(t, specific, "t", "x a b c d")
	ingest(t, specific, "t", "x z b c d")
	ingest(t, specific, "t", "x p q r e")
	if err := specific.SetTheta(60); err != nil {
		t.Fatal(err)
	}
	if r := ingest(t, specific, "t", "x q b r e"); r.ID != 2 {
		t.Fatalf("more specific template must win tie: %+v", r)
	}
	infos, _, _ = specific.Templates("t")
	if infos[1].Text != "x <*> <*> r e" || infos[0].Text != "x <*> b c d" {
		t.Fatalf("specificity tie result = %+v", infos)
	}
}

func TestOverflowDoesNotMergeNearTemplate(t *testing.T) {
	m, _ := New(75, 1, 1)
	ingest(t, m, "t", "x a b c")
	got := ingest(t, m, "t", "x p q c")
	if got.ID != 0 || !got.Overflow || got.Created {
		t.Fatalf("near template must overflow: %+v", got)
	}
	infos, overflow, _ := m.Templates("t")
	if overflow != 1 || infos[0].Count != 1 || infos[0].Text != "x a b c" {
		t.Fatalf("overflow mutated state: %+v overflow=%d", infos, overflow)
	}
	if _, err := m.Ingest("", "x"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestTenantIsolationAndLimits(t *testing.T) {
	m, _ := New(50, 10, 2)
	ingest(t, m, "a", "same message here")
	got := ingest(t, m, "b", "same message here")
	if got.ID != 1 || !got.Created {
		t.Fatalf("new tenant must restart ids: %+v", got)
	}
	_, err := m.Ingest("c", "same message here")
	if !errors.Is(err, ErrTenantLimit) {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := m.Templates("c"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatal(err)
	}
	var sum int64
	infos, overflow, _ := m.Templates("a")
	for _, info := range infos {
		sum += info.Count
	}
	if sum+overflow != 1 {
		t.Fatal("accepted count invariant")
	}

	limited, _ := New(50, 1, 1)
	ingest(t, limited, "a", "one valid message")
	_, err = limited.Ingest("b", "")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument must precede tenant limit: %v", err)
	}
	if _, _, err := limited.Templates("b"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("rejected Ingest registered tenant: %v", err)
	}
}

func TestSetThetaGenerationAndReplay(t *testing.T) {
	strict, _ := New(100, 10, 1)
	relaxed, _ := New(100, 10, 1)
	ingest(t, strict, "t", "x a b c")
	ingest(t, relaxed, "t", "x a b c")
	strictSecond := ingest(t, strict, "t", "x a q c")
	if err := relaxed.SetTheta(75); err != nil || relaxed.Gen() != 1 {
		t.Fatal(err)
	}
	relaxedSecond := ingest(t, relaxed, "t", "x a q c")
	if strictSecond.ID != 2 || relaxedSecond.ID != 1 {
		t.Fatalf("threshold changed destination: strict=%+v relaxed=%+v", strictSecond, relaxedSecond)
	}
	if err := strict.SetTheta(0); !errors.Is(err, ErrInvalidArgument) || strict.Gen() != 0 {
		t.Fatal("rejected SetTheta must not advance Gen")
	}
	infos, _, _ := strict.Templates("t")
	if infos[0].Text != "x a b c" || infos[0].Gen != 0 || infos[1].Gen != 0 {
		t.Fatalf("old templates were re-estimated: %+v", infos)
	}
}

func TestOnlyOwnLeafIsCompared(t *testing.T) {
	for _, others := range []int{1, 5000} {
		t.Run(fmt.Sprint(others), func(t *testing.T) {
			m, _ := New(100, others+1, 1)
			ingest(t, m, "t", "x target template")
			for i := 0; i < others; i++ {
				ingest(t, m, "t", fmt.Sprintf("y other %s z", alphaCode(i)))
			}
			infos, _, _ := m.Templates("t")
			if len(infos) != others+1 {
				t.Fatalf("setup created %d templates, want %d", len(infos), others+1)
			}
			m.resetLeafComparisons("t")
			ingest(t, m, "t", "x target changed")
			counts := m.leafComparisons("t")
			if counts["x"] != 1 {
				t.Fatalf("x comparisons=%d, counts=%v", counts["x"], counts)
			}
		})
	}
}

func alphaCode(value int) string {
	value++
	var chars []byte
	for value > 0 {
		value--
		chars = append([]byte{byte('a' + value%26)}, chars...)
		value /= 26
	}
	return string(chars)
}
