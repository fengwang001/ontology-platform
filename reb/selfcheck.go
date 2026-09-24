package reb

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/agg"
)

// SelfCheck 对内置操作序列核验四条不变量：第三节九步表、计数器上界、失败不留痕。
func SelfCheck() error {
	if err := checkNineSteps(); err != nil {
		return err
	}
	if err := checkCounterBound(); err != nil {
		return err
	}
	return checkRejectedNoOp()
}

// m 把 "a:2,b:1" 形式的串解析成计数映射，空串给空映射。
func m(s string) map[string]int {
	r := map[string]int{}
	if s == "" {
		return r
	}
	for _, p := range strings.Split(s, ",") {
		kv := strings.SplitN(p, ":", 2)
		n, _ := strconv.Atoi(kv[1])
		r[kv[0]] = n
	}
	return r
}

// 第三节九步表：src=["a","b","a","c","b","a"]、chunk=2，逐步核对
// processed / 检查点 / committed / gen；并核验不变式：building 时 shadow==cp.shadow，否则 shadow 已丢弃。
func checkNineSteps() error {
	r, err := New([]string{"a", "b", "a", "c", "b", "a"}, 2)
	if err != nil {
		return err
	}
	steps := []struct {
		op      func()
		proc    int
		cp, com string
		gen     int
	}{
		{func() {}, 0, "", "", 0},                                          // 1 New
		{func() { _ = r.Start() }, 0, "", "", 0},                           // 2 Start
		{func() { _ = r.Step() }, 2, "a:1,b:1", "", 0},                     // 3 Step
		{func() { _ = r.Step() }, 4, "a:2,b:1,c:1", "", 0},                 // 4 Step
		{func() { _ = r.Crash() }, 4, "a:2,b:1,c:1", "", 0},                // 5 Crash
		{func() {}, 4, "a:2,b:1,c:1", "", 0},                               // 6 View
		{func() { _ = r.Start() }, 4, "a:2,b:1,c:1", "", 0},                // 7 Start
		{func() { _ = r.Step() }, 6, "a:3,b:2,c:1", "", 0},                 // 8 Step
		{func() { _, _ = r.Commit() }, 6, "a:3,b:2,c:1", "a:3,b:2,c:1", 1}, // 9 Commit
	}
	for i, s := range steps {
		s.op()
		shadowOK := (!r.building && r.shadow == nil) || (r.building && agg.Equal(r.shadow, r.cp.shadow))
		if r.processed != s.proc || r.gen != s.gen || !shadowOK ||
			!agg.Equal(r.cp.shadow, m(s.cp)) || !agg.Equal(r.View(), m(s.com)) {
			return fmt.Errorf("nine-steps: step %d mismatch", i+1)
		}
	}
	return nil
}

// 计数器上界：n∈{1000,10000}、chunk=10、c=7 次崩溃，applied ≤ n+c·chunk，且视图与朴素重放一致。
func checkCounterBound() error {
	for _, n := range []int{1000, 10000} {
		src := make([]string, n)
		for i := range src {
			src[i] = fmt.Sprint(i % 64)
		}
		const chunk, crashes = 10, 7
		r, _ := New(src, chunk)
		for c := 0; c < crashes; c++ {
			_ = r.Start()
			_ = r.Step()
			_ = r.Crash()
		}
		_ = r.Start()
		for r.processed < n {
			_ = r.Step()
		}
		if _, err := r.Commit(); err != nil {
			return err
		}
		if r.applied > n+crashes*chunk {
			return fmt.Errorf("counter bound: n=%d applied=%d", n, r.applied)
		}
		if !agg.Equal(r.View(), agg.Replay(src)) {
			return fmt.Errorf("counter bound: n=%d view mismatch", n)
		}
	}
	return nil
}

// 失败不留痕：四类拒绝各给可判定错误，且状态不变、仍可继续使用。
func checkRejectedNoOp() error {
	if _, err := New([]string{"x"}, 0); !errors.Is(err, ErrBadChunk) {
		return fmt.Errorf("rejected: New chunk<1 not ErrBadChunk")
	}
	r, _ := New([]string{"x", "y"}, 1)
	if err := r.Step(); !errors.Is(err, ErrNotBuilding) {
		return fmt.Errorf("rejected: Step not ErrNotBuilding")
	}
	if _, err := r.Commit(); !errors.Is(err, ErrNotBuilding) {
		return fmt.Errorf("rejected: Commit not ErrNotBuilding")
	}
	if err := r.Crash(); !errors.Is(err, ErrNotBuilding) {
		return fmt.Errorf("rejected: Crash not ErrNotBuilding")
	}
	_ = r.Start()
	if err := r.Start(); !errors.Is(err, ErrBusy) {
		return fmt.Errorf("rejected: Start not ErrBusy")
	}
	if _, err := r.Commit(); !errors.Is(err, ErrIncomplete) {
		return fmt.Errorf("rejected: Commit not ErrIncomplete")
	}
	if r.processed != 0 || r.gen != 0 || len(r.View()) != 0 || !r.building {
		return fmt.Errorf("rejected: state changed")
	}
	return nil
}
