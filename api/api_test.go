package api_test

import (
	"errors"
	"maps"
	"math"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/eng"
	"ontology/wrap"
)

type step struct {
	ev api.Event // -1 表示该步被拒
	u  int64
}

// naive 是按规则从头离线逐步展开的朴素参照实现。
func naive(seq []uint32, th uint32) []step {
	out := make([]step, 0, len(seq))
	prev, pu := uint32(0), int64(0)
	for i, r := range seq {
		ev := api.Duplicate
		switch {
		case i == 0:
			ev, prev, pu = api.First, r, int64(r)
		case r == prev:
		case r > prev:
			ev, pu, prev = api.Forward, pu+int64(r)-int64(prev), r
		case prev-r > th:
			ev, pu, prev = api.Wrap, pu+(1<<32)-int64(prev)+int64(r), r
		default:
			ev = -1
		}
		out = append(out, step{ev, pu})
	}
	return out
}

// genSeq 生成含前进/重复/回卷/倒退的随机位点序列。
func genSeq(rnd *rand.Rand, n int) []uint32 {
	seq, cur := make([]uint32, 0, n), uint32(0)
	for len(seq) < n {
		switch x := rnd.Intn(10); {
		case x == 1 && cur > 5: // 小步倒退
			seq = append(seq, cur-uint32(1+rnd.Intn(5)))
			continue
		case x == 2: // 跳到回卷边界附近
			cur = math.MaxUint32 - uint32(rnd.Intn(100))
		case x >= 3: // 前进（自然越过 2^32 即回卷）
			cur += uint32(rnd.Intn(1000))
		}
		seq = append(seq, cur)
	}
	return seq
}

func TestNaiveReference(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		for seed := int64(0); seed < 3; seed++ {
			seq, th := genSeq(rand.New(rand.NewSource(seed)), n), uint32(1<<20)
			tr, _ := api.New(th)
			want := naive(seq, th)
			for i, r := range seq {
				ev, err := tr.Feed(r)
				if w := want[i]; w.ev < 0 {
					if !errors.Is(err, eng.ErrRewind) {
						t.Fatalf("i=%d: want rewind, got %v", i, err)
					}
				} else if u, _ := tr.LastUnwrapped(); err != nil || ev != w.ev || u != w.u {
					t.Fatalf("i=%d: got %v/%v/%d want %v/%d", i, ev, err, u, w.ev, w.u)
				}
			}
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	tr, _ := api.New(1 << 20)
	runs := []func() error{
		func() error { _, e := tr.LastUnwrapped(); return e },
		func() error { _, e := tr.LastRaw(); return e },
		func() error { _, e := api.New(0); return e },
		func() error { _, e := api.New(1 << 31); return e },
		func() error { _, e := wrap.Unwrap(math.MaxInt64-1, 100, 200, wrap.Forward); return e },
	}
	wants := []error{eng.ErrEmpty, eng.ErrEmpty, api.ErrThreshold, api.ErrThreshold, wrap.ErrOverflow}
	for i, run := range runs {
		if err := run(); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: got %v, want %v", i, err, wants[i])
		}
	}
	tr.Feed(100)
	c0 := tr.Counts()
	if _, err := tr.Feed(95); !errors.Is(err, eng.ErrRewind) {
		t.Fatal("want ErrRewind")
	}
	m := map[error]int{api.ErrThreshold: 1, eng.ErrRewind: 1, eng.ErrEmpty: 1, wrap.ErrOverflow: 1}
	if len(m) != 4 {
		t.Fatal("sentinel errors not distinct")
	}
	u1, _ := tr.LastUnwrapped()
	if raw, _ := tr.LastRaw(); u1 != 100 || raw != 100 || !maps.Equal(c0, tr.Counts()) {
		t.Fatal("rejections mutated state")
	}
	if _, err := tr.Feed(200); err != nil {
		t.Fatalf("feed after rejections: %v", err)
	}
}

// TestConcurrentReadOnly N 个 goroutine 并发只读同一个已喂满的实例，
// 各自拿到的 LastUnwrapped 与 Counts 必须逐字段相同。
func TestConcurrentReadOnly(t *testing.T) {
	tr, _ := api.New(1 << 20)
	for i := 0; i < 1000; i++ {
		tr.Feed(uint32(i))
	}
	type res struct {
		u int64
		c map[api.Event]int
	}
	ch := make(chan res, 16)
	for i := 0; i < 16; i++ {
		go func() {
			u, _ := tr.LastUnwrapped()
			if _, err := tr.LastRaw(); err != nil {
				t.Error(err)
			}
			if err := tr.SelfCheck(); err != nil {
				t.Error(err)
			}
			ch <- res{u, tr.Counts()}
		}()
	}
	want := <-ch
	for i := 1; i < 16; i++ {
		if r := <-ch; r.u != want.u || !maps.Equal(r.c, want.c) {
			t.Fatal("concurrent readers got different results")
		}
	}
}
