package api_test

import (
	"errors"
	"testing"

	"ontology/api"
)

// naive 是测试内独立的朴素单遍重算，返回每步分类。
func naive(seq []int64, thr, tol int64) []api.Class {
	out := make([]api.Class, len(seq))
	var last int64
	has := false
	for i, w := range seq {
		switch {
		case !has:
			has, last, out[i] = true, w, api.Normal
		case w > last+thr:
			last, out[i] = w, api.Drift
		case w >= last:
			last, out[i] = w, api.Normal
		case last-w <= tol:
			out[i] = api.Reorder
		default:
			out[i] = api.Rollback
		}
	}
	return out
}

// 确定性序列集：八步推导、纯递增、锯齿回退、公式伪随机。
func seqs() map[string][]int64 {
	m := map[string][]int64{"eight": {100, 105, 120, 118, 117, 116, 130, 141}}
	for i := 0; i < 300; i++ {
		m["inc"] = append(m["inc"], int64(i))
		m["saw"] = append(m["saw"], int64(50+(i%7)-3))
		m["rand"] = append(m["rand"], int64((i*i)%89+i/4))
	}
	return m
}

func feed(t *testing.T, d *api.Detector, seq []int64) []api.Class {
	t.Helper()
	out := make([]api.Class, len(seq))
	for i, w := range seq {
		var err error
		if out[i], err = d.Observe("s", w); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// 不变量 1：任意序列下每个源的 last 只进不退。
func TestMonotonicLast(t *testing.T) {
	for name, seq := range seqs() {
		d, _ := api.New(10, 3)
		prev := int64(-1)
		for _, w := range seq {
			if _, err := d.Observe("s", w); err != nil {
				t.Fatal(err)
			}
			last, _ := d.Last("s")
			if last < prev {
				t.Fatalf("%s: last %d < prev %d", name, last, prev)
			}
			prev = last
		}
	}
}

// 不变量 2：每条分类与朴素单遍重算逐条一致。
func TestClassifyMatchesNaive(t *testing.T) {
	for name, seq := range seqs() {
		d, _ := api.New(10, 3)
		want := naive(seq, 10, 3)
		for i, got := range feed(t, d, seq) {
			if got != want[i] {
				t.Fatalf("%s step %d: got %v, want %v", name, i, got, want[i])
			}
		}
	}
}

// 不变量 3：三计数各自等于对应类别数，总和等于非 Normal 事件数。
func TestCountsConserved(t *testing.T) {
	for name, seq := range seqs() {
		d, _ := api.New(10, 3)
		tally := map[api.Class]int64{}
		for _, c := range feed(t, d, seq) {
			tally[c]++
		}
		dn, rn, rb := d.Counts()
		if dn != tally[api.Drift] || rn != tally[api.Reorder] || rb != tally[api.Rollback] ||
			dn+rn+rb != int64(len(seq))-tally[api.Normal] {
			t.Fatalf("%s: counts %d,%d,%d not conserved", name, dn, rn, rb)
		}
	}
}

// 不变量 4 + 故障注入：三类错误可判定且互不相同，被拒后状态不变、仍可正常使用。
func TestRejectLeavesNoTrace(t *testing.T) {
	for _, p := range [][2]int64{{0, 1}, {-1, 1}, {1, -1}} {
		if _, err := api.New(p[0], p[1]); !errors.Is(err, api.ErrInvalidThreshold) {
			t.Fatalf("params %v not rejected", p)
		}
	}
	d, _ := api.New(10, 3)
	feed(t, d, seqs()["eight"])
	b1, b2, b3 := d.Counts()
	lb, _ := d.Last("s")
	_, e1 := d.Observe("", 5)
	_, e2 := d.Observe("s", -5)
	if !errors.Is(e1, api.ErrEmptySource) || !errors.Is(e2, api.ErrNegativeMark) ||
		errors.Is(e1, api.ErrNegativeMark) || errors.Is(e2, api.ErrEmptySource) {
		t.Fatal("reject errors not distinct")
	}
	a1, a2, a3 := d.Counts()
	la, _ := d.Last("s")
	if b1 != a1 || b2 != a2 || b3 != a3 || lb != la {
		t.Fatal("rejected op mutated state")
	}
	if _, err := d.Observe("s", 200); err != nil { // 被拒后仍可用
		t.Fatal("detector unusable after rejects")
	}
}

// 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	d, _ := api.New(10, 3)
	if err := d.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
