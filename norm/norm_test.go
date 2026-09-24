package norm

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func randSeq(seed int64, n int) []int64 {
	rng := rand.New(rand.NewSource(seed))
	ops := make([]int64, n)
	for i := range ops {
		v := int64(rng.Intn(4) + 1)
		if rng.Intn(2) == 0 {
			v = -v
		}
		ops[i] = v
	}
	return ops
}

// TestEightSteps 钉住第三节八行表：抵消与否、未了结序列、净值逐步核验。
func TestEightSteps(t *testing.T) {
	ops := []int64{5, -5, -8, 8, 7, 2, -7, -2}
	wantLog := [][]int64{{5}, {}, {-8}, {}, {7}, {7, 2}, {7, 2, -7}, {7, 2, -7, -2}}
	wantNet := []int64{5, 0, -8, 0, 7, 9, 2, 0}
	wantCancel := []bool{false, true, false, true, false, false, false, false}
	n := New(8)
	for i, op := range ops {
		if r, err := n.Apply(op); err != nil || r.Canceled != wantCancel[i] || !slices.Equal(n.Changelog(), wantLog[i]) || n.Net() != wantNet[i] {
			t.Fatalf("step %d: r=%v err=%v log=%v net=%d", i+1, r, err, n.Changelog(), n.Net())
		}
	}
}

// TestNaiveReplay 钉不变量 1：与朴素相邻抵消不动点逐条相同，净值等于代数和。
func TestNaiveReplay(t *testing.T) {
	check := func(t *testing.T, ops []int64, depth int) {
		n := New(depth)
		for _, op := range ops {
			if _, err := n.Apply(op); err != nil {
				t.Fatal(err)
			}
		}
		if got, sum := n.Changelog(), replaySum(ops); !slices.Equal(got, naiveForm(ops)) || n.Net() != sum {
			t.Fatalf("form=%v naive=%v net=%d sum=%d", got, naiveForm(ops), n.Net(), sum)
		}
	}
	for i, ops := range [][]int64{{5, -5, -8, 8, 7, 2, -7, -2}, {7, -7, 2, -2}} {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) { check(t, ops, 8) })
	}
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("rand%d", seed), func(t *testing.T) { check(t, randSeq(seed, 300), 512) })
	}
}

// TestFoldComplete 钉不变量 2：任何中间状态都不存在相邻反号等量对。
func TestFoldComplete(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			n := New(1024)
			for _, op := range randSeq(seed+100, 400) {
				if _, err := n.Apply(op); err != nil {
					t.Fatal(err)
				}
				if !foldComplete(n.Changelog()) {
					t.Fatalf("not fully folded: %v", n.Changelog())
				}
			}
		})
	}
}

// TestReplayEqualsNet 钉不变量 3：从 0 重放 changelog 恒等于 Net。
func TestReplayEqualsNet(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			n := New(1024)
			for _, op := range randSeq(seed+200, 400) {
				if _, err := n.Apply(op); err != nil {
					t.Fatal(err)
				}
				if replaySum(n.Changelog()) != n.Net() {
					t.Fatalf("replay=%d net=%d", replaySum(n.Changelog()), n.Net())
				}
			}
		})
	}
}

// TestRejectedNoTrace 钉不变量 4：三类拒绝可判定、互不相同、状态不变、仍可使用。
func TestRejectedNoTrace(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*Normalizer)
		op     int64
		follow int64 // 被拒后必须被接受的合法操作
		want   error
	}{
		{"zero", func(*Normalizer) {}, 0, 5, ErrInvalidIncrement},
		{"depth", func(n *Normalizer) { n.Apply(1); n.Apply(2) }, 3, -2, ErrDepthExceeded},
		{"overflow-max", func(n *Normalizer) { n.Apply(math.MaxInt64) }, 1, -1, ErrNetOverflow},
		{"overflow-min", func(n *Normalizer) { n.Apply(math.MinInt64) }, -1, 1, ErrNetOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := New(2)
			c.setup(n)
			snapNet, snapLog := n.Net(), n.Changelog()
			if _, err := n.Apply(c.op); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want=%v", err, c.want)
			}
			if n.Net() != snapNet || !slices.Equal(n.Changelog(), snapLog) {
				t.Fatalf("state changed after reject: net=%d log=%v", n.Net(), n.Changelog())
			}
			if _, err := n.Apply(c.follow); err != nil {
				t.Fatalf("not reusable after reject: %v", err)
			}
		})
	}
	if ErrInvalidIncrement == ErrDepthExceeded || ErrDepthExceeded == ErrNetOverflow || ErrInvalidIncrement == ErrNetOverflow {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

// TestTailChecksConstant 证明折叠只检查末尾一条：检查次数为与 m 无关的小常数。
// tailChecks 是非导出字段，仅本包（白盒）测试可直接读取，公开接口不暴露它。
func TestTailChecksConstant(t *testing.T) {
	n := New(20000)
	if r, err := n.Apply(1); err != nil || r.Canceled || n.tailChecks != 0 {
		t.Fatalf("first apply on empty: checks=%d r=%v err=%v", n.tailChecks, r, err)
	}
	for _, m := range []int{100, 1000, 10000} {
		q := New(m + 1)
		for i := 1; i <= m; i++ {
			if _, err := q.Apply(int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := q.Apply(-int64(m + 1)); err != nil { // 与末尾 +m 量值不等
			t.Fatal(err)
		}
		if q.tailChecks > 1 {
			t.Fatalf("m=%d tail checks=%d, want <= 1 (O(1))", m, q.tailChecks)
		}
	}
}
