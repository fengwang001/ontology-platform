package mig

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/keyenc"
)

func keysOf(t *testing.T, ents []Entry) []int64 {
	t.Helper()
	var keys []int64
	for _, e := range ents {
		keys = append(keys, e.Key)
	}
	return keys
}

func mustScan(t *testing.T, m *Migrator, lo, hi int64) []Entry {
	t.Helper()
	ents, err := m.Scan(lo, hi)
	if err != nil {
		t.Fatalf("Scan(%d,%d) err=%v", lo, hi, err)
	}
	return ents
}

func mustPut(t *testing.T, m *Migrator, k, v int64) {
	t.Helper()
	if err := m.Put(k, v); err != nil {
		t.Fatalf("Put(%d,%d) err=%v", k, v, err)
	}
}

func mustStep(t *testing.T, m *Migrator, n int) int {
	t.Helper()
	moved, err := m.Step(n)
	if err != nil {
		t.Fatalf("Step(%d) err=%v", n, err)
	}
	return moved
}

// 例一：物理首条是 1，逻辑最小的却是 -2；p=2 与 p=1 的残留与 Recover 计数。
func TestExample1(t *testing.T) {
	m := New()
	mustPut(t, m, -2, 10)
	mustPut(t, m, 1, 20)
	mustPut(t, m, 5, 30)

	if moved := mustStep(t, m, 1); moved != 1 {
		t.Fatalf("Step(1)=%d, want 1", moved)
	}
	if m.w != -1 {
		t.Fatalf("w=%d, want -1", m.w)
	}
	if got := keysOf(t, mustScan(t, m, -5, 6)); !reflect.DeepEqual(got, []int64{-2, 1, 5}) {
		t.Fatalf("Scan(-5,6) keys=%v, want [-2 1 5]", got)
	}

	if err := m.StepPartial(2); err != nil {
		t.Fatalf("StepPartial(2) err=%v", err)
	}
	if m.w != 2 {
		t.Fatalf("w=%d, want 2", m.w)
	}
	if got := keysOf(t, mustScan(t, m, 0, 6)); !reflect.DeepEqual(got, []int64{1, 5}) {
		t.Fatalf("crashed Scan(0,6) keys=%v, want [1 5]", got)
	}
	if v, ok, _ := m.Get(1); !ok || v != 20 {
		t.Fatalf("Get(1)=(%v,%v), want (20,true)", v, ok)
	}
	bDel, aDel, err := m.Recover()
	if err != nil || bDel != 0 || aDel != 1 {
		t.Fatalf("Recover=(%d,%d,%v), want (0,1,nil)", bDel, aDel, err)
	}

	if err := m.StepPartial(1); err != nil {
		t.Fatalf("StepPartial(1) err=%v", err)
	}
	if m.w != 2 {
		t.Fatalf("w=%d, want 2 (p=1 不推进水位)", m.w)
	}
	if v, ok, _ := m.Get(5); !ok || v != 30 {
		t.Fatalf("Get(5)=(%v,%v), want (30,true)，应走 A", v, ok)
	}
	bDel, aDel, err = m.Recover()
	if err != nil || bDel != 1 || aDel != 0 {
		t.Fatalf("Recover=(%d,%d,%v), want (1,0,nil)", bDel, aDel, err)
	}
	if got := keysOf(t, mustScan(t, m, -5, 6)); !reflect.DeepEqual(got, []int64{-2, 1, 5}) {
		t.Fatalf("final Scan keys=%v, want [-2 1 5]", got)
	}
}

// 例二：负数段的物理上界是无穷，而不是 E1(0)。
func TestExample2NegativeUpperInfinity(t *testing.T) {
	m := New()
	mustPut(t, m, -3, 1)
	mustPut(t, m, -1, 2)
	mustPut(t, m, 0, 3)
	mustPut(t, m, 2, 4)
	if got := keysOf(t, mustScan(t, m, -3, 0)); !reflect.DeepEqual(got, []int64{-3, -1}) {
		t.Fatalf("Scan(-3,0) keys=%v, want [-3 -1]（负数段上界须为无穷）", got)
	}
}

// 例三：Finish 前后路由；A 重新非空时 Finish 报 ErrNotDrained。
func TestExample3FinishRouting(t *testing.T) {
	m := New()
	mustPut(t, m, 0, 1)
	mustPut(t, m, 1, 1)
	mustPut(t, m, 2, 1)
	if moved := mustStep(t, m, 3); moved != 3 || m.w != 3 {
		t.Fatalf("Step(3) moved=%d w=%d, want 3/3", moved, m.w)
	}
	mustPut(t, m, 2, 100)
	if m.a.Len() != 0 {
		t.Fatalf("Put(2) 应走 B：a=%d", m.a.Len())
	}
	e2 := keyenc.E2(2)
	if v, ok := m.b.Get(e2[:]); !ok || v != 100 {
		t.Fatalf("B 中应有 (2,100), got (%v,%v)", v, ok)
	}
	mustPut(t, m, 3, 200)
	if m.a.Len() != 1 {
		t.Fatalf("Put(3) 应走 A：a=%d", m.a.Len())
	}
	ok, err := m.Delete(2)
	if err != nil || !ok {
		t.Fatalf("Delete(2)=(%v,%v), want (true,nil)", ok, err)
	}
	if moved := mustStep(t, m, 1); moved != 1 || m.w != 4 {
		t.Fatalf("Step(1) moved=%d w=%d, want 1/4（w 只是最后搬迁键加 1）", moved, m.w)
	}
	mustPut(t, m, 100, 300)
	if m.a.Len() != 1 {
		t.Fatalf("Put(100) 应走 A：a=%d", m.a.Len())
	}
	if err := m.Finish(); err != ErrNotDrained {
		t.Fatalf("Finish err=%v, want ErrNotDrained", err)
	}
	if moved := mustStep(t, m, 1); moved != 1 {
		t.Fatalf("Step(1)=%d, want 1", moved)
	}
	if err := m.Finish(); err != nil {
		t.Fatalf("Finish err=%v", err)
	}
	if m.w != MaxKey+1 {
		t.Fatalf("w=%d, want MaxKey+1", m.w)
	}
	mustPut(t, m, 7, 70)
	if m.b.Len() != 5 || m.a.Len() != 0 {
		t.Fatalf("Finish 后一切读写走 B：a=%d b=%d", m.a.Len(), m.b.Len())
	}
	if v, ok, _ := m.Get(7); !ok || v != 70 {
		t.Fatalf("Get(7)=(%v,%v), want (70,true)", v, ok)
	}
}

// 负数与非负数混合的扫描顺序：全程逻辑升序。
func TestScanMixedSignOrder(t *testing.T) {
	m := New()
	for _, k := range []int64{-5, -1, 0, 3, 7} {
		mustPut(t, m, k, k*10)
	}
	want := []int64{-5, -1, 0, 3, 7}
	if got := keysOf(t, mustScan(t, m, MinKey, MaxKey+1)); !reflect.DeepEqual(got, want) {
		t.Fatalf("full scan keys=%v, want %v", got, want)
	}
	if moved := mustStep(t, m, 2); moved != 2 || m.w != 0 {
		t.Fatalf("Step(2) moved=%d w=%d, want 2/0", moved, m.w)
	}
	ents := mustScan(t, m, MinKey, MaxKey+1)
	if got := keysOf(t, ents); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-step scan keys=%v, want %v", got, want)
	}
	for _, e := range ents {
		if e.Val != e.Key*10 {
			t.Fatalf("entry %+v 值不符", e)
		}
	}
}

// lo 或 hi 恰等于 w 与 0 的边界。
func TestScanBoundsAtWatermarkAndZero(t *testing.T) {
	m := New()
	mustPut(t, m, -1, 1)
	mustPut(t, m, 0, 2)
	mustPut(t, m, 1, 3)
	if moved := mustStep(t, m, 1); moved != 1 || m.w != 0 {
		t.Fatalf("Step(1) moved=%d w=%d, want 1/0", moved, m.w)
	}
	if got := keysOf(t, mustScan(t, m, -1, 0)); !reflect.DeepEqual(got, []int64{-1}) {
		t.Fatalf("hi==w==0: keys=%v, want [-1]", got)
	}
	if got := keysOf(t, mustScan(t, m, 0, 2)); !reflect.DeepEqual(got, []int64{0, 1}) {
		t.Fatalf("lo==w==0: keys=%v, want [0 1]", got)
	}
	if got := keysOf(t, mustScan(t, m, 0, 0)); len(got) != 0 {
		t.Fatalf("lo==hi==w: keys=%v, want 空", got)
	}
	if got := keysOf(t, mustScan(t, m, -5, 0)); !reflect.DeepEqual(got, []int64{-1}) {
		t.Fatalf("hi==w: keys=%v, want [-1]", got)
	}
	if got := keysOf(t, mustScan(t, m, 0, 5)); !reflect.DeepEqual(got, []int64{0, 1}) {
		t.Fatalf("lo==w: keys=%v, want [0 1]", got)
	}
}

// 搬迁中途 Get/Put/Delete 的归属：k 恰为 w-1 与 w。
func TestRoutingAtWatermarkBoundary(t *testing.T) {
	m := New()
	for k := int64(0); k <= 2; k++ {
		mustPut(t, m, k, k)
	}
	mustStep(t, m, 3) // w=3
	mustPut(t, m, 2, 22)
	mustPut(t, m, 3, 33)
	e2 := keyenc.E2(2)
	if _, ok := m.b.Get(e2[:]); !ok {
		t.Fatal("k=w-1=2 应在 B")
	}
	e1 := keyenc.E1(3)
	if _, ok := m.a.Get(e1[:]); !ok {
		t.Fatal("k=w=3 应在 A")
	}
	if v, ok, _ := m.Get(2); !ok || v != 22 {
		t.Fatalf("Get(2)=(%v,%v), want (22,true)", v, ok)
	}
	if v, ok, _ := m.Get(3); !ok || v != 33 {
		t.Fatalf("Get(3)=(%v,%v), want (33,true)", v, ok)
	}
	if ok, _ := m.Delete(2); !ok {
		t.Fatal("Delete(2) 应为 true（删自 B）")
	}
	if _, ok, _ := m.Get(2); ok {
		t.Fatal("Delete 后 Get(2) 应不存在")
	}
	if ok, _ := m.Delete(2); ok {
		t.Fatal("重复 Delete(2) 应为 false")
	}
	if ok, _ := m.Delete(3); !ok {
		t.Fatal("Delete(3) 应为 true（删自 A）")
	}
}

// 崩溃态拒绝：Put/Delete/Step/StepPartial/Finish 报 ErrCrashed，Get/Scan 仍可用。
func TestCrashedRejection(t *testing.T) {
	m := New()
	mustPut(t, m, 1, 10)
	mustPut(t, m, 2, 20)
	if err := m.StepPartial(1); err != nil {
		t.Fatalf("StepPartial(1) err=%v", err)
	}
	if err := m.Put(3, 30); err != ErrCrashed {
		t.Fatalf("Put err=%v, want ErrCrashed", err)
	}
	if _, err := m.Delete(1); err != ErrCrashed {
		t.Fatalf("Delete err=%v, want ErrCrashed", err)
	}
	if _, err := m.Step(1); err != ErrCrashed {
		t.Fatalf("Step err=%v, want ErrCrashed", err)
	}
	if err := m.StepPartial(1); err != ErrCrashed {
		t.Fatalf("StepPartial err=%v, want ErrCrashed", err)
	}
	if err := m.Finish(); err != ErrCrashed {
		t.Fatalf("Finish err=%v, want ErrCrashed", err)
	}
	if _, _, err := m.Get(1); err != nil {
		t.Fatalf("崩溃态 Get err=%v", err)
	}
	if _, err := m.Scan(0, 10); err != nil {
		t.Fatalf("崩溃态 Scan err=%v", err)
	}
	if _, _, err := m.Recover(); err != nil {
		t.Fatalf("Recover err=%v", err)
	}
	if err := m.Put(3, 30); err != nil {
		t.Fatalf("恢复后 Put err=%v", err)
	}
}

// 被拒操作不改任何存储与水位。
func TestRejectionLeavesStateUnchanged(t *testing.T) {
	m := New()
	for _, k := range []int64{-2, 0, 4} {
		mustPut(t, m, k, k)
	}
	mustStep(t, m, 1) // w=-1
	snapshot := func() string {
		ents, _ := m.Scan(MinKey, MaxKey+1)
		return strings.Join([]string{
			fmt.Sprint(ents), fmt.Sprint(m.w), fmt.Sprint(m.a.Len()), fmt.Sprint(m.b.Len()), fmt.Sprint(m.crashed),
		}, "|")
	}
	before := snapshot()
	rejects := []func() error{
		func() error { return m.Put(MinKey-1, 1) },
		func() error { return m.Put(MaxKey+1, 1) },
		func() error { _, err := m.Delete(MinKey - 1); return err },
		func() error { _, _, err := m.Get(MaxKey + 1); return err },
		func() error { _, err := m.Scan(5, 3); return err },
		func() error { _, err := m.Scan(MinKey-1, 0); return err },
		func() error { _, err := m.Scan(0, MaxKey+2); return err },
		func() error { _, err := m.Step(0); return err },
		func() error { _, err := m.Step(10001); return err },
		func() error { return m.StepPartial(0) },
		func() error { return m.StepPartial(3) },
		func() error { _, _, err := m.Recover(); return err },
		func() error { return m.Finish() },
	}
	wantErrs := []error{
		ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid,
		ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid, ErrNotCrashed, ErrNotDrained,
	}
	for i, fn := range rejects {
		if err := fn(); err != wantErrs[i] {
			t.Fatalf("第 %d 个被拒操作 err=%v, want %v", i, err, wantErrs[i])
		}
		if after := snapshot(); after != before {
			t.Fatalf("第 %d 个被拒操作改变了状态：%s -> %s", i, before, after)
		}
	}
	if err := m.StepPartial(1); err != nil {
		t.Fatalf("StepPartial err=%v", err)
	}
	crashedSnap := snapshot()
	crashedRejects := []func() error{
		func() error { return m.Put(1, 1) },
		func() error { _, err := m.Delete(1); return err },
		func() error { _, err := m.Step(1); return err },
		func() error { return m.StepPartial(2) },
		func() error { return m.Finish() },
	}
	for i, fn := range crashedRejects {
		if err := fn(); err != ErrCrashed {
			t.Fatalf("崩溃态第 %d 个操作 err=%v, want ErrCrashed", i, err)
		}
		if after := snapshot(); after != crashedSnap {
			t.Fatalf("崩溃态第 %d 个被拒操作改变了状态", i)
		}
	}
}

// Step 为每个搬迁键探测 A 的条目数不超过 2，与 A 的规模无关。
func TestStepProbeBudget(t *testing.T) {
	m := New()
	for k := int64(-1000); k < 1000; k++ {
		mustPut(t, m, k, k)
	}
	moved := mustStep(t, m, 500)
	if moved != 500 {
		t.Fatalf("moved=%d, want 500", moved)
	}
	if m.probes > 2*moved {
		t.Fatalf("probes=%d > 2*%d", m.probes, moved)
	}
	moved = mustStep(t, m, 10000)
	if moved != 1500 {
		t.Fatalf("moved=%d, want 1500", moved)
	}
	if m.probes > 2*moved {
		t.Fatalf("drain probes=%d > 2*%d", m.probes, moved)
	}
	if got := mustStep(t, m, 1); got != 0 {
		t.Fatalf("A 空时 Step 应返回 0, got %d", got)
	}
}

// Scan 的物理产出条数恰等于返回条数（含崩溃残留存在时）。
func TestScanPhysicalCount(t *testing.T) {
	m := New()
	for _, k := range []int64{-4, -1, 0, 3, 6} {
		mustPut(t, m, k, k)
	}
	mustStep(t, m, 2) // w=-3
	check := func(lo, hi int64) {
		t.Helper()
		before := m.scanPhys
		ents, err := m.Scan(lo, hi)
		if err != nil {
			t.Fatalf("Scan(%d,%d) err=%v", lo, hi, err)
		}
		if got := m.scanPhys - before; got != len(ents) {
			t.Fatalf("Scan(%d,%d) 物理产出 %d != 返回 %d", lo, hi, got, len(ents))
		}
	}
	check(MinKey, MaxKey+1)
	check(-3, 0)
	check(0, 7)
	if err := m.StepPartial(1); err != nil { // B 中留下 >=w 的残留
		t.Fatalf("StepPartial err=%v", err)
	}
	check(MinKey, MaxKey+1)
	check(-2, 5)
	if _, _, err := m.Recover(); err != nil {
		t.Fatalf("Recover err=%v", err)
	}
	check(MinKey, MaxKey+1)
}

// 并发调用等价于某个串行顺序；Scan 与逐键 Get 一致。
func TestConcurrentLinearizable(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				k := int64(r.Intn(101) - 50)
				switch r.Intn(8) {
				case 0:
					m.Put(k, int64(r.Intn(1000)))
				case 1:
					m.Get(k)
				case 2:
					m.Delete(k)
				case 3:
					lo := int64(r.Intn(101) - 50)
					m.Scan(lo, lo+int64(r.Intn(20)))
				case 4:
					m.Step(1 + r.Intn(5))
				case 5:
					m.StepPartial(1 + r.Intn(2))
				case 6:
					m.Recover()
				default:
					m.Finish()
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	if _, _, err := m.Recover(); err != nil && err != ErrNotCrashed {
		t.Fatalf("Recover err=%v", err)
	}
	ents, err := m.Scan(MinKey, MaxKey+1)
	if err != nil {
		t.Fatalf("final Scan err=%v", err)
	}
	for i := 1; i < len(ents); i++ {
		if ents[i-1].Key >= ents[i].Key {
			t.Fatalf("Scan 结果非严格升序：%v", ents)
		}
	}
	for _, e := range ents {
		v, ok, err := m.Get(e.Key)
		if err != nil || !ok || v != e.Val {
			t.Fatalf("Get(%d)=(%v,%v,%v) 与 Scan 值 %v 不一致", e.Key, v, ok, err, e.Val)
		}
	}
}
