package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/slot"
	"ontology/wal"
)

func rec(l int64, k wal.Kind, x, d string) wal.Record { return wal.Rec(l, k, x, d) }
func bg(l int64, x string) wal.Record                 { return rec(l, wal.Begin, x, "") }
func ct(l int64, x string) wal.Record                 { return rec(l, wal.Commit, x, "") }

func ap(rs ...wal.Record) func(*api.API) error {
	return func(a *api.API) error { return a.Append(rs...) }
}
func cf(l int64) func(*api.API) error { return func(a *api.API) error { return a.Confirm(l) } }

// 第三节 E1–E8：操作、期望错误、操作后期望的 confirmed/restart。
var ops = []func(*api.API) error{ap(bg(10, "T1"), bg(20, "T2"), rec(25, wal.Change, "T2", "x"), ct(30, "T1")), cf(30),
	ap(bg(40, "T3"), ct(50, "T3")), cf(45),
	ap(bg(60, "T4"), rec(70, wal.Abort, "T4", ""), rec(75, wal.Change, "T2", "y"), ct(80, "T2")), cf(50), func(a *api.API) error { a.Restart(); return nil }, cf(80)}
var stepErr = []error{nil, nil, nil, slot.ErrNotBoundary, nil, nil, nil, nil}
var want = [][2]int64{{5, 5}, {30, 20}, {30, 20}, {30, 20}, {30, 20}, {50, 20}, {50, 20}, {80, 80}}

func run(t *testing.T, upto int) *api.API {
	a, lc, lr := api.New(10), int64(0), int64(0)
	for i := 0; i < upto; i++ {
		err := ops[i](a)
		c, r := a.ConfirmedLSN(), a.RestartLSN()
		if !errors.Is(err, stepErr[i]) || c != want[i][0] || r != want[i][1] || c < lc || r < lr || r > c {
			t.Fatalf("E%d err=%v c=%d r=%d（表值 %v/%d/%d 且单调有序）", i+1, err, c, r, stepErr[i], want[i][0], want[i][1])
		}
		lc, lr = c, r
	}
	return a
}

// 不变量 1：8 步表与朴素推导一致；随机交错序列与内置朴素参照一致。
func TestNaiveRef(t *testing.T) {
	run(t, 8)
	if err := api.New(10).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 不变量 2：任意时刻重启，重发集合恰为已提交且提交 LSN > confirmed 的事务。
func TestRestartRecover(t *testing.T) {
	names := []string{"E7崩溃重发T2", "E5后E6前崩溃重发T3T2", "E2后崩溃无重发"}
	upto := []int{6, 5, 2}
	wants := [][]slot.Txn{
		{{Xid: "T2", CommitLSN: 80, Changes: []string{"x", "y"}}},
		{{Xid: "T3", CommitLSN: 50}, {Xid: "T2", CommitLSN: 80, Changes: []string{"x", "y"}}},
		nil,
	}
	for i := range names {
		a := run(t, upto[i])
		a.Restart()
		if got := a.Emitted(); !reflect.DeepEqual(got, wants[i]) {
			t.Fatalf("%s: 重发=%v 期望=%v", names[i], got, wants[i])
		}
	}
	if err := api.New(10).SelfCheck(); err != nil { // 内含随机时刻重启核验
		t.Fatal(err)
	}
}

// 不变量 3：confirmed 与 restart 单调不减且 restart <= confirmed。
func TestMonotonic(t *testing.T) { run(t, 8) }

// 不变量 4：四类可判定错误互不相同，被拒后状态不变且仍可正常使用。
func TestRejectNoTrace(t *testing.T) {
	bad := []func(*api.API) error{ap(bg(5, "C")), ap(bg(30, "A")), ap(rec(30, wal.Change, "Z", "d")), cf(35), cf(3), ap(bg(30, "C")), ap(ct(30, "A"), rec(31, wal.Change, "ZZ", "d"))}
	wants := []error{wal.ErrLSNOrder, wal.ErrXidOpen, wal.ErrXidNotOpen, slot.ErrNotBoundary, slot.ErrBackward, slot.ErrTooManyOpen, wal.ErrXidNotOpen}
	for i := range bad {
		a := api.New(2)
		a.Append(bg(10, "A"), bg(20, "B"))
		s0 := fmt.Sprint(a.ConfirmedLSN(), a.RestartLSN(), a.Emitted())
		if err := bad[i](a); !errors.Is(err, wants[i]) {
			t.Fatalf("case%d: err=%v want %v", i, err, wants[i])
		}
		if fmt.Sprint(a.ConfirmedLSN(), a.RestartLSN(), a.Emitted()) != s0 {
			t.Fatal(i, ": 被拒操作改变了状态")
		}
		if err := a.Append(ct(30, "A")); err != nil {
			t.Fatal(i, ": 被拒后不可继续使用:", err)
		}
	}
	if err := api.New(2).Confirm(5); err != nil { // 幂等确认
		t.Fatal(err)
	}
}

// 并发：一边按序追加交错事务，一边按提交顺序确认，M 个读者读到的 LSN 单调且有序。
func TestConcurrent(t *testing.T) {
	a, n := api.New(100), 200
	var bad, stop int32
	var wg, rd sync.WaitGroup
	wg.Add(2)
	go func() { // 追加交错事务
		defer wg.Done()
		for i, lsn := 0, int64(10); i <= n; i, lsn = i+1, lsn+3 {
			if i < n {
				a.Append(bg(lsn+1, fmt.Sprintf("T%d", i)), rec(lsn+2, wal.Change, fmt.Sprintf("T%d", i), "v"))
			}
			if i > 0 {
				a.Append(ct(lsn+3, fmt.Sprintf("T%d", i-1)))
			}
		}
	}()
	go func() { // 按提交顺序逐个确认
		defer wg.Done()
		for seen := 0; seen < n; seen++ {
			var em []slot.Txn
			for em = a.Emitted(); seen >= len(em); em = a.Emitted() {
			}
			if a.Confirm(em[seen].CommitLSN) != nil {
				atomic.StoreInt32(&bad, 1)
				return
			}
		}
	}()
	for k := 0; k < 4; k++ { // 读者：先 r 后 c，读到因果一致的一对
		rd.Add(1)
		go func() {
			defer rd.Done()
			lc, lr, ok := int64(0), int64(0), true
			for atomic.LoadInt32(&stop) == 0 {
				r, c := a.RestartLSN(), a.ConfirmedLSN()
				ok = ok && c >= lc && r >= lr && r <= c
				lc, lr = c, r
			}
			if !ok {
				atomic.StoreInt32(&bad, 1)
			}
		}()
	}
	wg.Wait()
	atomic.StoreInt32(&stop, 1)
	rd.Wait()
	if atomic.LoadInt32(&bad) != 0 {
		t.Fatal("并发下不变量被破坏")
	}
}
