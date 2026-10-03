package booking

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func mustNew(t *testing.T, c int, stocks []int64, rho int) *Book {
	t.Helper()
	b, err := New(c, stocks, rho)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func contractIDs(cs []Contract) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func snapshotState(b *Book) string {
	return fmt.Sprintf("booked=%v waiting=%v", b.BookedContracts(), b.WaitingContracts())
}

// 规格示例：C=3, [100,50,80], rho=90。
func TestSpecExample(t *testing.T) {
	b := mustNew(t, 3, []int64{100, 50, 80}, 90)
	caps := map[uint32]int64{
		0b001: 90, 0b010: 45, 0b100: 72,
		0b011: 135, 0b101: 162, 0b110: 117, 0b111: 207,
	}
	for mask, want := range caps {
		if got := b.Capacity(mask); got != want {
			t.Fatalf("Cap(%03b)=%d want %d", mask, got, want)
		}
	}

	if r := b.Book("a", 0b001, 80); !r.OK {
		t.Fatalf("book a: %v", r.Reason)
	}
	r := b.Book("b", 0b011, 60)
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) || r.Phase != PhaseWaiting {
		t.Fatalf("book b: %+v", r)
	}
	if r = b.Book("c", 0b010, 40); !r.OK {
		t.Fatalf("book c: %v", r.Reason)
	}
	r = b.Cancel("a")
	if !r.OK || len(r.Promoted) != 1 || r.Promoted[0] != "b" {
		t.Fatalf("cancel a promoted=%v", r.Promoted)
	}
	if w := contractIDs(b.WaitingContracts()); len(w) != 0 {
		t.Fatalf("waiting=%v", w)
	}
	if !b.Feasible() {
		t.Fatal("state infeasible")
	}
}

// qty 恰等于 Cap(mask) 可订；多 1 永不可订、不进候补、不计判定子集。
func TestExactlyCapAndNeverBookable(t *testing.T) {
	b := mustNew(t, 1, []int64{100}, 90)
	if r := b.Book("eq", 1, 90); !r.OK {
		t.Fatalf("qty==Cap: %v", r.Reason)
	}

	b2 := mustNew(t, 1, []int64{100}, 90)
	r := b2.Book("over", 1, 91)
	if r.OK || !errors.Is(r.Reason, ErrNeverBookable) {
		t.Fatalf("qty=Cap+1: %+v", r)
	}
	if len(b2.BookedContracts()) != 0 || len(b2.WaitingContracts()) != 0 {
		t.Fatal("never-bookable must not enter any queue")
	}
	if r.SubsetsChecked != 0 || b2.CounterSubsets() != 0 {
		t.Fatalf("single-point judgment must not count subsets")
	}
}

// 子集负载恰等于 Cap(T) 可行；多 1 不可行进候补（非永不可订）。
func TestSubsetLoadExactly(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	if r := b.Book("x", 0b11, 150); !r.OK {
		t.Fatal(r.Reason)
	}
	if r := b.Book("y", 0b01, 50); !r.OK { // {0,1}: 200==Cap
		t.Fatalf("load==Cap should be feasible: %v", r.Reason)
	}

	b3 := mustNew(t, 2, []int64{100, 100}, 100)
	b3.Book("x", 0b11, 150)
	r := b3.Book("z", 0b01, 51) // 201>200；单格 51<=100 故不是永不可订
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) || r.Phase != PhaseWaiting {
		t.Fatalf("load=Cap+1: %+v", r)
	}
}

// 整体取整与逐单元取整不同：[105,55], rho=90 => 94/49/144（逐单元和为 143）。
func TestFloorOnAggregate(t *testing.T) {
	b := mustNew(t, 2, []int64{105, 55}, 90)
	if b.Capacity(0b01) != 94 || b.Capacity(0b10) != 49 || b.Capacity(0b11) != 144 {
		t.Fatalf("caps=%d,%d,%d", b.Capacity(1), b.Capacity(2), b.Capacity(3))
	}
	if r := b.Book("a", 0b11, 144); !r.OK {
		t.Fatalf("aggregate floor 144 should book (per-cell sum would be 143): %v", r.Reason)
	}

	b2 := mustNew(t, 2, []int64{105, 55}, 90)
	r := b2.Book("b", 0b11, 145)
	if r.OK || !errors.Is(r.Reason, ErrNeverBookable) {
		t.Fatalf("145 exceeds aggregate Cap 144: %+v", r)
	}
}

// 跨单元定向：单元 1 全空看似充足，交叉子集 {0,1} 不可行。
func TestCrossTargetInfeasible(t *testing.T) {
	// [100,50], rho=90：Cap {0}=90、{1}=45、{0,1}=135。
	// a {0}=80；b {0,1}=60 单看 60<=135 可订，但交叉子集 80+60=140>135。
	b := mustNew(t, 2, []int64{100, 50}, 90)
	if r := b.Book("a", 0b01, 80); !r.OK {
		t.Fatal(r.Reason)
	}
	r := b.Book("b", 0b11, 60)
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) || r.Phase != PhaseWaiting {
		t.Fatalf("cross-target: %+v", r)
	}
}

// 候补不阻塞队首：释放后队首仍不可行，其后的项照常递补。
func TestWaitingDoesNotBlockHead(t *testing.T) {
	// p {0,1}=190；h {0,1}=51 候补；q {1}=40 候补（全集 230>200）。
	b := mustNew(t, 2, []int64{100, 100}, 100)
	b.Book("p", 0b11, 190)
	if r := b.Book("h", 0b11, 51); r.Phase != PhaseWaiting {
		t.Fatalf("h should wait: %+v", r)
	}
	if r := b.Book("q", 0b10, 40); r.Phase != PhaseWaiting {
		t.Fatalf("q should wait: %+v", r)
	}
	// p 缩到 160：h 160+51=211>200 仍不可行；q {1}=40、全集 160+40=200 可行。
	r := b.Resize("p", 160)
	if !r.OK || len(r.Promoted) != 1 || r.Promoted[0] != "q" {
		t.Fatalf("only tail q should promote, got %v", r.Promoted)
	}
	if w := contractIDs(b.WaitingContracts()); len(w) != 1 || w[0] != "h" {
		t.Fatalf("head h should remain waiting, got %v", w)
	}
}

// 递补先到先排，且本轮已递补者占用容量影响后续。
func TestPromotionOrderAndCoupling(t *testing.T) {
	// a=80；w1=30、w2=25 候补。取消 a 后按序递补 w1,w2。
	b := mustNew(t, 1, []int64{100}, 100)
	b.Book("a", 1, 80)
	if r := b.Book("w1", 1, 30); r.Phase != PhaseWaiting {
		t.Fatalf("w1: %+v", r)
	}
	if r := b.Book("w2", 1, 25); r.Phase != PhaseWaiting {
		t.Fatalf("w2: %+v", r)
	}
	r := b.Cancel("a")
	if len(r.Promoted) != 2 || r.Promoted[0] != "w1" || r.Promoted[1] != "w2" {
		t.Fatalf("promoted=%v", r.Promoted)
	}

	// 耦合：a=100；w1=100、w2=1 候补。w1 递补后 w2=101>100 必须留下。
	b2 := mustNew(t, 1, []int64{100}, 100)
	b2.Book("a", 1, 100)
	b2.Book("w1", 1, 100)
	b2.Book("w2", 1, 1)
	r = b2.Cancel("a")
	if len(r.Promoted) != 1 || r.Promoted[0] != "w1" {
		t.Fatalf("promoted=%v", r.Promoted)
	}
	if w := contractIDs(b2.WaitingContracts()); len(w) != 1 || w[0] != "w2" {
		t.Fatalf("w2 must stay waiting, got %v", w)
	}

	// 扫描后不变量：每个候补项单独加入当前集合都不可行。
	for _, w := range b2.WaitingContracts() {
		if b2.waitingWouldFit(w.Mask, w.Qty) {
			t.Fatalf("waiting %s became feasible after sweep", w.ID)
		}
	}
}

// Resize：相等空操作；变小触发递补；变大成功/拒绝（不进候补、不改状态）。
func TestResize(t *testing.T) {
	b := mustNew(t, 1, []int64{100}, 100)
	b.Book("a", 1, 100)
	b.Book("w", 1, 10)

	r := b.Resize("a", 100)
	if !r.OK || len(r.Promoted) != 0 || r.SubsetsChecked != 0 {
		t.Fatalf("equal resize must be no-op: %+v", r)
	}

	r = b.Resize("a", 90)
	if !r.OK || len(r.Promoted) != 1 || r.Promoted[0] != "w" {
		t.Fatalf("smaller resize should promote w: %+v", r)
	}

	r = b.Book("c", 1, 5) // w=10 已递补：90+10+5=105>100 -> c 进候补
	if r.OK || r.Phase != PhaseWaiting {
		t.Fatalf("c should wait: %+v", r)
	}
	before := snapshotState(b)
	r = b.Resize("a", 96) // 96+10=106>100
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) {
		t.Fatalf("larger resize must be rejected: %+v", r)
	}
	if after := snapshotState(b); after != before {
		t.Fatalf("rejected resize changed state:\nbefore=%s\nafter =%s", before, after)
	}
	r = b.Resize("a", 95) // 95+10=105>100，仍拒绝且不改状态
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) {
		t.Fatalf("resize to 95 still infeasible: %+v", r)
	}
	if after := snapshotState(b); after != before {
		t.Fatalf("rejected resize changed state:\nbefore=%s\nafter =%s", before, after)
	}
	r = b.Resize("a", 85) // 85+10=95，释放容量后 c=5 递补
	if !r.OK || len(r.Promoted) != 1 || r.Promoted[0] != "c" {
		t.Fatalf("shrinking to 85 should promote c: %+v", r)
	}
}

// Retarget：相同定向空操作；扩大被拒不改状态；缩小成功并触发递补。
func TestRetarget(t *testing.T) {
	// 相同定向：空操作，不检查子集，不触发递补。
	b := mustNew(t, 2, []int64{100, 100}, 100)
	b.Book("a", 0b01, 60)
	r := b.Retarget("a", 0b01)
	if !r.OK || r.SubsetsChecked != 0 || len(r.Promoted) != 0 {
		t.Fatalf("same-mask retarget must be no-op: %+v", r)
	}

	// 交叉改定向（新旧不可比）：a {0}=60、b {1}=60；a 改到 {1} 时
	// 只检查 T={1}（包含 mask2 而不含旧 mask），60+60=120>100 拒绝。
	bf := mustNew(t, 2, []int64{100, 100}, 100)
	bf.Book("a", 0b01, 60)
	bf.Book("b", 0b10, 60)
	before := snapshotState(bf)
	r = bf.Retarget("a", 0b10)
	if r.OK || !errors.Is(r.Reason, ErrInfeasible) || r.SubsetsChecked != 1 {
		t.Fatalf("cross retarget must be rejected after checking exactly 1 subset: %+v", r)
	}
	if after := snapshotState(bf); after != before {
		t.Fatalf("rejected retarget changed state:\nbefore=%s\nafter =%s", before, after)
	}

	// 纯扩大：a {0}=60 扩到 {0,1}，只需检查新增 T={1}（不含旧 mask）。
	be := mustNew(t, 2, []int64{100, 100}, 100)
	be.Book("a", 0b01, 60)
	be.ResetCounters()
	if r = be.Retarget("a", 0b11); !r.OK || r.SubsetsChecked != 0 {
		t.Fatalf("pure expand checks no new constrained subset: %+v", r)
	}
	if be.CounterSubsets() != 0 || be.CounterCalls() != 0 {
		t.Fatalf("pure expand must not perform feasibility judgments, got subsets=%d calls=%d",
			be.CounterSubsets(), be.CounterCalls())
	}

	// 缩小触发递补：p {0}=30 已订；c {0}=71 候补（{0}=101>100）。
	// p 改定向到 {1}（缩小且去掉单元 0）后，{0} 释放为 0，c=71 递补。
	b2 := mustNew(t, 2, []int64{100, 100}, 100)
	b2.Book("p", 0b01, 30)
	if r := b2.Book("c", 0b01, 71); r.Phase != PhaseWaiting {
		t.Fatalf("c: %+v", r)
	}
	r = b2.Retarget("p", 0b10)
	if !r.OK || len(r.Promoted) != 1 || r.Promoted[0] != "c" {
		t.Fatalf("shrink retarget should promote c: %+v", r)
	}
}

// Cancel 候补项：直接移除，不触发递补。
func TestCancelWaitingNoPromotion(t *testing.T) {
	b := mustNew(t, 1, []int64{100}, 100)
	b.Book("a", 1, 100)
	b.Book("w1", 1, 10)
	b.Book("w2", 1, 10)
	r := b.Cancel("w1")
	if !r.OK || len(r.Promoted) != 0 || r.SubsetsChecked != 0 {
		t.Fatalf("cancel waiting must not promote: %+v", r)
	}
	if w := contractIDs(b.WaitingContracts()); len(w) != 1 || w[0] != "w2" {
		t.Fatalf("waiting=%v, order must be preserved", w)
	}
}

// 各类拒绝的优先级：按规格固定顺序只报第一个。
func TestRejectionPriority(t *testing.T) {
	b := mustNew(t, 1, []int64{100}, 100)
	b.Book("a", 1, 10)
	b.Book("w", 1, 100)

	// Book：非法参数优先于已存在。
	if r := b.Book("a", 0, 0); !errors.Is(r.Reason, ErrInvalid) {
		t.Fatalf("book invalid priority: %+v", r)
	}
	if r := b.Book("a", 1, 5); !errors.Is(r.Reason, ErrAlreadyExists) {
		t.Fatalf("book duplicate: %+v", r)
	}
	if r := b.Book("w", 1, 5); !errors.Is(r.Reason, ErrAlreadyExists) {
		t.Fatalf("book duplicate waiting: %+v", r)
	}

	// Cancel：非法 id 优先于不存在。
	if r := b.Cancel(""); !errors.Is(r.Reason, ErrInvalid) {
		t.Fatalf("cancel empty: %+v", r)
	}
	if r := b.Cancel("nope"); !errors.Is(r.Reason, ErrNotFound) {
		t.Fatalf("cancel missing: %+v", r)
	}

	// Resize：非法 -> 不存在 -> 未预订。
	if r := b.Resize("", 1); !errors.Is(r.Reason, ErrInvalid) {
		t.Fatalf("resize empty: %+v", r)
	}
	if r := b.Resize("nope", 1); !errors.Is(r.Reason, ErrNotFound) {
		t.Fatalf("resize missing: %+v", r)
	}
	if r := b.Resize("w", 1); !errors.Is(r.Reason, ErrNotBooked) {
		t.Fatalf("resize waiting: %+v", r)
	}

	// Retarget：非法 -> 不存在 -> 未预订。
	if r := b.Retarget("", 1); !errors.Is(r.Reason, ErrInvalid) {
		t.Fatalf("retarget empty: %+v", r)
	}
	if r := b.Retarget("nope", 1); !errors.Is(r.Reason, ErrNotFound) {
		t.Fatalf("retarget missing: %+v", r)
	}
	if r := b.Retarget("w", 1); !errors.Is(r.Reason, ErrNotBooked) {
		t.Fatalf("retarget waiting: %+v", r)
	}

	// 非法 mask 越界（含 0）、qty 越界（0 与 1e12+1）。
	for _, m := range []uint32{0, 2, 1 << 31} {
		if r := b.Book("z", m, 1); !errors.Is(r.Reason, ErrInvalid) {
			t.Fatalf("mask=%d: %+v", m, r)
		}
	}
	for _, q := range []int64{0, 1_000_000_000_001} {
		if r := b.Book("z", 1, q); !errors.Is(r.Reason, ErrInvalid) {
			t.Fatalf("qty=%d: %+v", q, r)
		}
		if r := b.Resize("a", q); !errors.Is(r.Reason, ErrInvalid) {
			t.Fatalf("resize qty=%d: %+v", q, r)
		}
	}
}

// 构造参数非法。
func TestNewInvalid(t *testing.T) {
	good := []int64{1}
	if _, err := New(0, good, 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("C=0: %v", err)
	}
	if _, err := New(11, make([]int64, 11), 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("C=11: %v", err)
	}
	if _, err := New(2, []int64{1, -1}, 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative stock: %v", err)
	}
	if _, err := New(1, []int64{1_000_000_000_001}, 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge stock: %v", err)
	}
	if _, err := New(1, good, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rho=0: %v", err)
	}
	if _, err := New(1, good, 1001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rho=1001: %v", err)
	}
	if _, err := New(2, []int64{1}, 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stocks length mismatch: %v", err)
	}
}

// 边界量级：1e12 库存、1000% 超订不溢出。
func TestLargeQuantities(t *testing.T) {
	b := mustNew(t, 1, []int64{1_000_000_000_000}, 1000)
	if cap := b.Capacity(1); cap != 10_000_000_000_000 {
		t.Fatalf("cap=%d", cap)
	}
	if r := b.Book("a", 1, 1_000_000_000_000); !r.OK {
		t.Fatal(r.Reason)
	}
	if !b.Feasible() {
		t.Fatal("infeasible")
	}
}

var _ = sort.Strings
var _ = sync.WaitGroup{}
