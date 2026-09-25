package api

import (
	"maps"
	"math/rand"
	"ontology/off"
	"testing"
)

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

// replay 逐步执行 {kind: 0=Commit 1=Checkpoint 2=Evict, p, off, ts/now, wantC, wantRec} 并核验。
func replay(t *testing.T, v V, steps [][6]int64) {
	for i, s := range steps {
		p := int(s[1])
		ops := []func(int64, int64) error{
			func(off, ts int64) error { return v.Commit(p, off, ts) },
			func(_, _ int64) error { return v.Checkpoint(p) },
			func(_, now int64) error { return v.Evict(p, now) },
		}
		if err := ops[s[0]](s[2], s[3]); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if c, _ := v.Committed(p); c != s[4] || v.Restart()[p] != s[5] {
			t.Fatalf("step %d: want %d/%d", i+1, s[4], s[5])
		}
	}
}

// 不变量1：八步精确序列（第 5 步即 (丙) 严格小于边界，第 8 步即检查点永不驱逐）。
func TestEightStepTrace(t *testing.T) {
	v, _ := New(10)
	replay(t, v, [][6]int64{
		{0, 0, 100, 0, 100, 100}, {1, 0, 0, 0, 100, 100}, {0, 0, 110, 10, 110, 110}, {0, 0, 120, 15, 120, 120},
		{2, 0, 0, 20, 120, 120}, {0, 0, 130, 30, 130, 130}, {2, 0, 0, 40, 130, 130}, {2, 0, 0, 45, 130, 100},
	})
	w, _ := New(10) // (丙) 边界可观测化：归档最大条目 (110,10) 在阈值 10 下幸存
	replay(t, w, [][6]int64{{0, 0, 100, 0, 100, 100}, {1, 0, 0, 0, 100, 100}, {0, 0, 110, 10, 110, 110}, {2, 0, 0, 20, 110, 110}})
}

// 不变量1：随机操作序列下 Restart 与独立镜像模型的批量重算一致。
func TestRestartMatchesBatch(t *testing.T) {
	type md struct {
		cp, off, ts int64
		arc         [][2]int64
	}
	for _, seed := range []int64{1, 7, 42, 2026} {
		v, _ := New(10)
		rng, now := rand.New(rand.NewSource(seed)), int64(0)
		replay(t, v, [][6]int64{{0, 0, 1, 0, 1, 1}, {0, 1, 1, 0, 1, 1}, {0, 2, 1, 0, 1, 1}})
		ms := [3]md{{cp: off.NegInf, off: 1, arc: [][2]int64{{1, 0}}}, {cp: off.NegInf, off: 1, arc: [][2]int64{{1, 0}}}, {cp: off.NegInf, off: 1, arc: [][2]int64{{1, 0}}}}
		for range 200 {
			switch p, what := rng.Intn(3), rng.Intn(3); what {
			case 0:
				ms[p].off += 1 + rng.Int63n(5)
				ms[p].ts += rng.Int63n(3)
				must(t, v.Commit(p, ms[p].off, ms[p].ts))
				ms[p].arc = append(ms[p].arc, [2]int64{ms[p].off, ms[p].ts})
			case 1:
				must(t, v.Checkpoint(p))
				ms[p].cp = ms[p].off
			default:
				now += rng.Int63n(8)
				must(t, v.Evict(p, now))
				for len(ms[p].arc) > 0 && ms[p].arc[0][1] < now-10 {
					ms[p].arc = ms[p].arc[1:]
				}
			}
			for p, m := range ms { // 批量重算：max(cp, 幸存归档最大位点)
				r := m.cp
				if n := len(m.arc); n > 0 && m.arc[n-1][0] > r {
					r = m.arc[n-1][0]
				}
				if v.Restart()[p] != r {
					t.Fatalf("seed=%d p=%d: got %d want %d", seed, p, v.Restart()[p], r)
				}
			}
		}
	}
}

// 不变量3：最新未检查点归档幸存时，恢复位点单调不减（此处恒等于最新位点，严格递增）。
func TestRecoverMonotonic(t *testing.T) {
	v, _ := New(10)
	for i := int64(0); i < 30; i++ {
		must(t, v.Commit(0, 100+i, i))
		if i%3 == 2 {
			must(t, v.Checkpoint(0))
		}
		must(t, v.Evict(0, i)) // 阈值 i-10 < i，最新归档幸存
		if r := v.Restart()[0]; r != 100+i {
			t.Fatalf("i=%d: rec=%d want %d", i, r, 100+i)
		}
	}
}

// 不变量4：四类哨兵错误互不相同；被拒后状态不变且仍可正常使用。
func TestRejectLeavesNoTrace(t *testing.T) {
	if _, err := New(0); err != ErrInvalidRetention {
		t.Fatal("non-positive retention accepted")
	}
	if len(map[error]bool{ErrInvalidRetention: true, ErrNonMonotonic: true, ErrNowRegression: true, ErrNoCommit: true}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	ops := []func(V) error{
		func(v V) error { return v.Commit(0, 100, 50) }, // off 不增
		func(v V) error { return v.Commit(0, 200, -1) }, // ts 回退
		func(v V) error { return v.Evict(0, 10) },       // now 回退（上次 45）
		func(v V) error { return v.Checkpoint(9) },      // 无已提交位点
	}
	wants := []error{ErrNonMonotonic, ErrNonMonotonic, ErrNowRegression, ErrNoCommit}
	for i, op := range ops {
		v, _ := New(10)
		replay(t, v, [][6]int64{{0, 0, 100, 0, 100, 100}, {1, 0, 0, 0, 100, 100}, {0, 0, 130, 30, 130, 130}, {2, 0, 0, 45, 130, 100}})
		before := v.Restart()
		if err := op(v); err != wants[i] {
			t.Fatalf("case %d: err=%v want %v", i, err, wants[i])
		}
		if !maps.Equal(before, v.Restart()) {
			t.Fatalf("case %d: state changed after rejection", i)
		}
		must(t, v.Commit(0, 140, 50)) // 仍可正常使用
	}
}
func TestConcurrentReads(t *testing.T) {
	v, _ := New(10)
	replay(t, v, [][6]int64{{0, 0, 100, 0, 100, 100}, {1, 0, 0, 0, 100, 100}, {0, 0, 130, 30, 130, 130}, {2, 0, 0, 45, 130, 100}, {0, 1, 7, 0, 7, 7}, {1, 1, 0, 0, 7, 7}})
	start, bad := make(chan struct{}), make(chan bool, 8)
	for range 8 {
		go func() {
			<-start
			b := false
			for range 100 {
				c, _ := v.Committed(0)
				b = b || c != 130 || v.Restart()[0] != 100 || v.Restart()[1] != 7 || v.SelfCheck() != nil
			}
			bad <- b
		}()
	}
	close(start)
	for range 8 {
		if <-bad {
			t.Fatal("inconsistent concurrent read")
		}
	}
}
