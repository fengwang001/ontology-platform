package deadlock

import (
	"reflect"
	"testing"
)

func mustNew(t *testing.T, R int, T, c []int64, P, L int) *Manager {
	t.Helper()
	m, err := New(R, T, c, P, L)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func mustRequest(t *testing.T, m *Manager, p int, alts [][]int64) RequestResult {
	t.Helper()
	res, err := m.Request(p, alts)
	if err != nil {
		t.Fatalf("Request(%d, %v): %v", p, alts, err)
	}
	return res
}

func mustGrant(t *testing.T, m *Manager, p int, alts [][]int64, wantAlt int) {
	t.Helper()
	res := mustRequest(t, m, p, alts)
	if !res.Granted || res.Alt != wantAlt {
		t.Fatalf("Request(%d, %v) = %+v, want granted alt %d", p, alts, res, wantAlt)
	}
}

func mustBlock(t *testing.T, m *Manager, p int, alts [][]int64) {
	t.Helper()
	res := mustRequest(t, m, p, alts)
	if res.Granted {
		t.Fatalf("Request(%d, %v) = %+v, want blocked", p, alts, res)
	}
}

func mustRelease(t *testing.T, m *Manager, p int, vec []int64) []Grant {
	t.Helper()
	grants, err := m.Release(p, vec)
	if err != nil {
		t.Fatalf("Release(%d, %v): %v", p, vec, err)
	}
	return grants
}

func wantErrCode(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %v, got nil", code)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error %v is not *Error", err)
	}
	if e.Code != code {
		t.Fatalf("error code = %v, want %v (err=%v)", e.Code, code, err)
	}
}

// TestSpecExample 完整复现题目示例。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, 2, []int64{3, 2}, []int64{1, 10}, 4, 2)

	mustGrant(t, m, 0, [][]int64{{2, 0}}, 0)
	mustGrant(t, m, 1, [][]int64{{0, 2}}, 0)
	mustGrant(t, m, 2, [][]int64{{1, 0}}, 0)
	mustBlock(t, m, 0, [][]int64{{0, 1}})
	mustBlock(t, m, 1, [][]int64{{1, 0}, {0, 1}})

	// 未阻塞的进程 2、3 视为会结束，进程 1、0 可被归约。
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect = %v, want empty", got)
	}

	mustBlock(t, m, 2, [][]int64{{0, 1}})
	if got := m.Detect(); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("Detect = %v, want [0 1 2]", got)
	}

	steps := m.Resolve()
	want := []ResolveStep{{
		Victim:     2,
		Cost:       1,
		Grants:     []Grant{{PID: 1, Alt: 0}},
		Terminated: false,
	}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Resolve = %+v, want %+v", steps, want)
	}
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect after Resolve = %v, want empty", got)
	}

	grants := mustRelease(t, m, 1, []int64{0, 2})
	if !reflect.DeepEqual(grants, []Grant{{PID: 0, Alt: 0}}) {
		t.Fatalf("Release grants = %v, want [{0 0}]", grants)
	}
}

// TestFirstFittingAltChosen 第一个可满足备选被选中，而非“最合适”的。
func TestFirstFittingAltChosen(t *testing.T) {
	m := mustNew(t, 2, []int64{5, 5}, []int64{1, 1}, 2, 2)
	// 两个备选都放得下，必须选列在前的 [2,2] 而不是更小的 [1,1]。
	mustGrant(t, m, 0, [][]int64{{2, 2}, {1, 1}}, 0)
	// 第一个放不下、第二个放得下，选第二个。
	mustGrant(t, m, 1, [][]int64{{4, 4}, {1, 1}}, 1)
}

// TestBlockOnlyWhenNoAltFits 备选全放不下才阻塞，且不部分授予。
func TestBlockOnlyWhenNoAltFits(t *testing.T) {
	m := mustNew(t, 2, []int64{2, 2}, []int64{1, 1}, 2, 2)
	mustGrant(t, m, 0, [][]int64{{2, 2}}, 0)
	// 全部备选都放不下才阻塞。
	mustBlock(t, m, 1, [][]int64{{1, 0}, {0, 1}, {1, 1}})
	// 不部分授予：阻塞的进程 1 不持有任何新资源，且处于阻塞态。
	_, err := m.Release(1, []int64{1, 0})
	wantErrCode(t, err, ErrProcessBlocked)
	// 释放 [1,1] 后授予不动点选中第一个可满足备选（下标 0）。
	grants := mustRelease(t, m, 0, []int64{1, 1})
	if !reflect.DeepEqual(grants, []Grant{{PID: 1, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{1 0}]", grants)
	}
	// 进程 1 现在恰持有 [1,0]。
	mustRelease(t, m, 1, []int64{1, 0})
}
