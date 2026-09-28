package sampler

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// testLogger 收集每一步日志并转发到测试输出（go test -v 可见）。
type testLogger struct {
	t  *testing.T
	mu sync.Mutex
}

func (l *testLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.t.Logf(format, args...)
}

func newTestSampler(t *testing.T, k int, vals []float64) (*Sampler, *Sequence) {
	t.Helper()
	seq := NewSequence(vals)
	s, err := New(k, seq, WithLogger(&testLogger{t: t}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, seq
}

func idsOf(ss []Selected) []string {
	out := make([]string, len(ss))
	for i, x := range ss {
		out[i] = x.Element.ID
	}
	return out
}

func eqStrings(a, b []string) bool {
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

// 批量排序参照：对全部元素统一算键值，按键值降序（并列先到者优先）取前 k 个。
func batchReference(t *testing.T, k int, elems []Element, vals []float64) []string {
	t.Helper()
	if len(elems) != len(vals) {
		t.Fatalf("reference setup: %d elems vs %d randoms", len(elems), len(vals))
	}
	all := make([]Selected, len(elems))
	for i, e := range elems {
		all[i] = Selected{Element: e, Key: keyOf(e.Weight, vals[i]), Order: i}
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && ranksHigher(all[j], all[j-1]); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > k {
		all = all[:k]
	}
	return idsOf(all)
}

func TestBatchReferenceConsistency(t *testing.T) {
	elems := []Element{
		{"a", 1}, {"b", 2.5}, {"c", 0.3}, {"d", 7}, {"e", 1.2},
		{"f", 4}, {"g", 0.8}, {"h", 3.3}, {"i", 0.1}, {"j", 5.5},
	}
	vals := []float64{0.11, 0.42, 0.87, 0.03, 0.66, 0.25, 0.91, 0.5, 0.72, 0.09}

	for _, k := range []int{1, 3, 5, 10} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			s, seq := newTestSampler(t, k, vals)
			for _, e := range elems {
				if _, err := s.Submit(e.ID, e.Weight); err != nil {
					t.Fatalf("Submit %s: %v", e.ID, err)
				}
			}
			if err := s.Check(); err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got := s.Consumed(); got != len(elems) {
				t.Fatalf("consumed=%d want %d", got, len(elems))
			}
			if seq.Consumed() != len(elems) || seq.Remaining() != 0 {
				t.Fatalf("source cursor: consumed=%d remaining=%d", seq.Consumed(), seq.Remaining())
			}
			want := batchReference(t, k, elems, vals)
			if got := idsOf(s.Samples()); !eqStrings(got, want) {
				t.Fatalf("stream=%v batch=%v", got, want)
			}
		})
	}
}

func TestTiedKeysEarlierWins(t *testing.T) {
	// 0.25^(1/1) = 0.25；0.0625^(1/2) = 0.25，两元素键值严格相等。
	t.Run("first arrives first", func(t *testing.T) {
		s, _ := newTestSampler(t, 1, []float64{0.25, 0.0625})
		if ok, err := s.Submit("early", 1); err != nil || !ok {
			t.Fatalf("early: ok=%v err=%v", ok, err)
		}
		if ok, err := s.Submit("late", 2); err != nil || ok {
			t.Fatalf("late must lose the tie: ok=%v err=%v", ok, err)
		}
		if got := idsOf(s.Samples()); !eqStrings(got, []string{"early"}) {
			t.Fatalf("got %v want [early]", got)
		}
	})
	t.Run("reverse arrival order", func(t *testing.T) {
		s, _ := newTestSampler(t, 1, []float64{0.0625, 0.25})
		if ok, err := s.Submit("early", 2); err != nil || !ok {
			t.Fatalf("early: ok=%v err=%v", ok, err)
		}
		if ok, err := s.Submit("late", 1); err != nil || ok {
			t.Fatalf("late must lose the tie: ok=%v err=%v", ok, err)
		}
		if got := idsOf(s.Samples()); !eqStrings(got, []string{"early"}) {
			t.Fatalf("got %v want [early]", got)
		}
	})
}

func TestZeroWeightRejectedAndNoTrace(t *testing.T) {
	s, seq := newTestSampler(t, 2, []float64{0.4, 0.5})
	ok, err := s.Submit("zero", 0)
	if !errors.Is(err, ErrZeroWeight) || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if s.Consumed() != 0 || seq.Consumed() != 0 || len(s.Samples()) != 0 {
		t.Fatal("zero-weight submission left a trace")
	}
	// 游标未动：后续正常元素仍使用第一个随机数。
	if ok, err := s.Submit("good", 1); err != nil || !ok {
		t.Fatalf("good: ok=%v err=%v", ok, err)
	}
	got := s.Samples()
	if len(got) != 1 || got[0].Element.ID != "good" || got[0].Key != 0.4 {
		t.Fatalf("unexpected sample after zero-weight reject: %+v", got)
	}
}

func TestRandomSourceExhausted(t *testing.T) {
	s, seq := newTestSampler(t, 3, []float64{0.2, 0.7})
	if _, err := s.Submit("a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit("b", 1); err != nil {
		t.Fatal(err)
	}
	before := idsOf(s.Samples())
	ok, err := s.Submit("c", 1)
	if !errors.Is(err, ErrRandomsExhausted) || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if seq.Consumed() != 2 || s.Consumed() != 2 {
		t.Fatalf("cursor moved after exhaustion: %d/%d", seq.Consumed(), s.Consumed())
	}
	if !eqStrings(idsOf(s.Samples()), before) {
		t.Fatalf("samples changed after exhaustion: %v vs %v", idsOf(s.Samples()), before)
	}
	if _, err := s.Submit("d", 1); !errors.Is(err, ErrRandomsExhausted) {
		t.Fatalf("repeated draw error mismatch: %v", err)
	}
}

func TestIllegalRandomValueRejectedAndNoTrace(t *testing.T) {
	// 非法随机数必须留在原处不被跳过：游标不动，提交失败不留痕。
	s, seq := newTestSampler(t, 2, []float64{0.5, 1.5})
	if _, err := s.Submit("a", 1); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Submit("b", 1)
	if !errors.Is(err, ErrIllegalRandom) || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if seq.Consumed() != 1 || s.Consumed() != 1 {
		t.Fatalf("cursor advanced over illegal value: %d/%d", seq.Consumed(), s.Consumed())
	}
	if got := idsOf(s.Samples()); !eqStrings(got, []string{"a"}) {
		t.Fatalf("samples changed: %v", got)
	}
	if _, err := s.Submit("b", 1); !errors.Is(err, ErrIllegalRandom) {
		t.Fatalf("expected ErrIllegalRandom again, got %v", err)
	}
}

func TestIllegalInputsHaveDistinctReasons(t *testing.T) {
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"sample size zero", func() error { _, e := New(0, NewSequence(nil)); return e }, ErrInvalidSampleSize},
		{"sample size negative", func() error { _, e := New(-2, NewSequence(nil)); return e }, ErrInvalidSampleSize},
		{"nil source", func() error { _, e := New(2, nil); return e }, ErrIllegalRandom},
		{"empty id", func() error {
			s, _ := newTestSampler(t, 2, []float64{0.5})
			_, e := s.Submit("", 1)
			return e
		}, ErrEmptyID},
		{"negative weight", func() error {
			s, _ := newTestSampler(t, 2, []float64{0.5})
			_, e := s.Submit("x", -1)
			return e
		}, ErrIllegalWeight},
		{"NaN weight", func() error {
			s, _ := newTestSampler(t, 2, []float64{0.5})
			_, e := s.Submit("x", math.NaN())
			return e
		}, ErrIllegalWeight},
		{"infinite weight", func() error {
			s, _ := newTestSampler(t, 2, []float64{0.5})
			_, e := s.Submit("x", math.Inf(1))
			return e
		}, ErrIllegalWeight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("%s: err=%v want %v", tc.name, err, tc.want)
			}
		})
	}

	// 重复标识不消耗随机数。
	s, seq := newTestSampler(t, 2, []float64{0.3, 0.4})
	if _, err := s.Submit("dup", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit("dup", 2); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want ErrDuplicateID, got %v", err)
	}
	if s.Consumed() != 1 || seq.Consumed() != 1 {
		t.Fatalf("duplicate submission consumed a random: %d/%d", s.Consumed(), seq.Consumed())
	}

	// 七个错误类别两两可区分。
	sentinels := []error{
		ErrInvalidSampleSize, ErrEmptyID, ErrDuplicateID, ErrIllegalWeight,
		ErrZeroWeight, ErrIllegalRandom, ErrRandomsExhausted,
	}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("error categories %v and %v are not distinguishable", sentinels[i], sentinels[j])
			}
		}
	}
}

func TestRejectionLeavesStateUnchanged(t *testing.T) {
	s, seq := newTestSampler(t, 2, []float64{0.15, 0.85, 0.4, 0.6})
	for _, e := range []Element{{"a", 1}, {"b", 1}} {
		if _, err := s.Submit(e.ID, e.Weight); err != nil {
			t.Fatalf("Submit %s: %v", e.ID, err)
		}
	}
	snapshot := idsOf(s.Samples())

	for _, b := range []Element{{"", 1}, {"a", 1}, {"c", 0}, {"d", -3}, {"e", math.NaN()}} {
		if _, err := s.Submit(b.ID, b.Weight); err == nil {
			t.Fatalf("expected rejection for id=%q w=%v", b.ID, b.Weight)
		}
	}
	if !eqStrings(idsOf(s.Samples()), snapshot) {
		t.Fatalf("samples changed after rejections: %v vs %v", idsOf(s.Samples()), snapshot)
	}
	if s.Consumed() != 2 || seq.Consumed() != 2 {
		t.Fatalf("cursor changed after rejections: sampler=%d source=%d", s.Consumed(), seq.Consumed())
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}
