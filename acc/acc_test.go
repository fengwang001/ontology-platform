package acc

import (
	"errors"
	"math/rand"
	"testing"
)

type in struct{ off, delta int64 }

func applyAll(t *testing.T, a *Acc, rs []in) {
	t.Helper()
	for _, r := range rs {
		if err := a.Apply("k", r.off, r.delta); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommitStopsAtGap(t *testing.T) {
	cases := []struct {
		name            string
		first, second   []in // second 在第一次 Commit 后 Apply
		wantCp, wantSum int64
		wantPending     []int64
	}{
		{"contiguous", []in{{0, 10}, {1, 20}, {2, 30}}, nil, 2, 60, nil},
		{"gap at 1", []in{{0, 10}, {2, 20}, {3, 30}}, nil, 0, 10, []int64{2, 3}},
		{"no zero", []in{{1, 20}, {2, 30}}, nil, -1, 0, []int64{1, 2}},
		{"gap filled later", []in{{0, 10}, {2, 20}, {3, 30}}, []in{{1, 40}}, 3, 100, nil},
		{"still gapped", []in{{0, 10}, {2, 20}}, []in{{4, 50}}, 0, 10, []int64{2, 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := New(16)
			applyAll(t, a, c.first)
			a.Commit()
			for _, r := range c.second {
				applyAll(t, a, []in{r})
				a.Commit()
			}
			if a.cp != c.wantCp || a.sum["k"] != c.wantSum || len(a.pending) != len(c.wantPending) {
				t.Fatalf("cp=%d sum=%d pending=%v", a.cp, a.sum["k"], a.pending)
			}
			for _, o := range c.wantPending {
				if _, ok := a.pending[o]; !ok {
					t.Fatalf("pending missing offset %d", o)
				}
			}
		})
	}
}

func TestIdempotentAndRestore(t *testing.T) {
	a, _ := New(8)
	applyAll(t, a, []in{{0, 10}, {2, 20}})
	a.Commit() // cp=0, sum=10, pending{2:20}
	if err := errors.Join(a.Apply("k", 0, 999), a.Apply("k", 2, 999)); err != nil {
		t.Fatal(err) // 已持久化 / 在途重复，均幂等跳过
	}
	if a.cp != 0 || a.sum["k"] != 10 || len(a.pending) != 1 || a.pending[2].delta != 20 {
		t.Fatalf("duplicate apply changed state: cp=%d sum=%v pending=%v", a.cp, a.sum, a.pending)
	}
	a.Restore() // 崩溃：pending 丢失，sum/cp 保留
	if a.cp != 0 || a.sum["k"] != 10 || len(a.pending) != 0 {
		t.Fatalf("restore: cp=%d sum=%v pending=%v", a.cp, a.sum, a.pending)
	}
	applyAll(t, a, []in{{1, 40}, {2, 20}}) // 源端从 cp+1 重投
	a.Commit()
	if a.cp != 2 || a.sum["k"] != 70 {
		t.Fatalf("replay: cp=%d sum=%d, want 2/70", a.cp, a.sum["k"])
	}
}

func TestApplyErrorsNoSideEffect(t *testing.T) {
	if errors.Is(ErrEmptyKey, ErrNegativeOffset) || errors.Is(ErrNegativeOffset, ErrTooManyPending) ||
		errors.Is(ErrEmptyKey, ErrTooManyPending) {
		t.Fatal("sentinel errors must be distinct")
	}
	cases := []struct {
		name, key string
		off       int64
		want      error
	}{
		{"empty key", "", 5, ErrEmptyKey},
		{"negative offset", "k", -1, ErrNegativeOffset},
		{"overflow", "k", 5, ErrTooManyPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := New(2)
			applyAll(t, a, []in{{0, 10}, {1, 20}}) // pending 满
			if err := a.Apply(c.key, c.off, 99); !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			if a.cp != -1 || a.sum["k"] != 0 || len(a.pending) != 2 {
				t.Fatalf("rejected apply left trace: cp=%d sum=%v pending=%v", a.cp, a.sum, a.pending)
			}
			a.Commit() // 被拒后仍可正常使用
			applyAll(t, a, []in{{2, 30}})
		})
	}
}

func TestDedupCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(2 * m)
		for i := int64(0); i < int64(m); i++ {
			if err := a.Apply("k", i, 1); err != nil {
				t.Fatal(err)
			}
		}
		for _, off := range []int64{int64(m), int64(m) / 2} { // 全新 / 在途重复
			if err := a.Apply("k", off, 1); err != nil {
				t.Fatal(err)
			}
			if a.lastChecks > 1 {
				t.Fatalf("m=%d off=%d: dedup checked %d pending entries, want <=1", m, off, a.lastChecks)
			}
		}
	}
}

func TestNaiveReference(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, _ := New(64)
		applied := map[int64]int64{}
		ncp, nsum := int64(-1), int64(0)
		for i := 0; i < 300; i++ {
			off, delta := rng.Int63n(40), rng.Int63n(100)
			if err := a.Apply("k", off, delta); err != nil {
				t.Fatal(err)
			}
			if _, dup := applied[off]; !dup {
				applied[off] = delta
			}
			if rng.Intn(4) == 0 {
				a.Commit()
				for d, ok := applied[ncp+1]; ok; d, ok = applied[ncp+1] { // 参照只在提交点推进
					nsum += d
					ncp++
				}
			}
			if a.cp != ncp || a.sum["k"] != nsum {
				t.Fatalf("seed=%d op=%d: cp=%d/%d sum=%d/%d", seed, i, a.cp, ncp, a.sum["k"], nsum)
			}
		}
	}
}
