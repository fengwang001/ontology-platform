// Package api 是对外门面：串行化所有操作，暴露只读视图与自检。依赖 lside。
package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"strings"
	"sync"

	"ontology/lside"
	"ontology/rside"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey       = rside.ErrEmptyKey
	ErrEmptyQueue     = rside.ErrEmptyQueue
	ErrTooManyPending = rside.ErrTooManyPending
)

type API struct {
	mu sync.RWMutex
	r  *rside.Right
	l  *lside.Left
}

func New(maxPending int) *API {
	r := rside.New(maxPending)
	return &API{r: r, l: lside.New(r)}
}
func (a *API) write(f func() error) error { a.mu.Lock(); defer a.mu.Unlock(); return f() }
func read[T any](a *API, f func() T) T    { a.mu.RLock(); defer a.mu.RUnlock(); return f() }
func (a *API) PutLeft(k, fk, v string) error {
	return a.write(func() error { return a.l.PutLeft(k, fk, v) })
}
func (a *API) DeleteLeft(k string) error    { return a.write(func() error { return a.l.DeleteLeft(k) }) }
func (a *API) PutRight(fk, rv string) error { return a.write(func() error { return a.r.Put(fk, rv) }) }
func (a *API) DeleteRight(fk string) error  { return a.write(func() error { return a.r.Delete(fk) }) }
func (a *API) Deliver(fk string) error      { return a.write(func() error { return a.l.Deliver(fk) }) }
func (a *API) View() map[string][2]string   { return read(a, a.l.View) }
func (a *API) Changelog() []string          { return read(a, a.l.Changelog) }
func (a *API) Discarded() int               { return read(a, a.l.Discarded) }

// DrainAll 反复投递直到所有队列为空。
func (a *API) DrainAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for fks := a.r.PendingFKs(); len(fks) > 0; fks = a.r.PendingFKs() {
		for _, fk := range fks {
			_ = a.l.Deliver(fk)
		}
	}
}

// batchJoin 从零做一次内连接（不变量 1 的参照）。
func batchJoin(left map[string]lside.Row, right map[string]string) map[string][2]string {
	out := make(map[string][2]string)
	for k, row := range left {
		if rv, ok := right[row.FK]; row.FK != "" && ok {
			out[k] = [2]string{row.Val, rv}
		}
	}
	return out
}

// SelfCheck 在全新实例上跑内置操作序列，核验四条不变量；不触碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	inst := New(1 << 20)
	rng := rand.New(rand.NewSource(7))
	applied, logLen, err := map[string][2]string{}, 0, error(nil)
	for i := 0; i < 500; i++ {
		k, fk := fmt.Sprintf("k%d", rng.Intn(9)), fmt.Sprintf("f%d", rng.Intn(4))
		ops := []func(){
			func() { _ = inst.PutLeft(k, fk, fmt.Sprintf("v%d", rng.Intn(3))) }, func() { _ = inst.PutLeft(k, "", "v") },
			func() { _ = inst.DeleteLeft(k) }, func() { _ = inst.PutRight(fk, fmt.Sprintf("r%d", rng.Intn(3))) },
			func() { _ = inst.DeleteRight(fk) }, func() { _ = inst.Deliver(fk) },
		}
		ops[rng.Intn(len(ops))]()
		if applied, logLen, err = applyLog(applied, inst.Changelog(), logLen); err != nil {
			return err // 不变量 2：每个前缀自洽
		}
		if err := inst.l.CheckSubs(); err != nil {
			return err // 不变量 3：订阅一致
		}
	}
	inst.DrainAll()
	if applied, _, err = applyLog(applied, inst.Changelog(), logLen); err != nil {
		return err
	}
	rtab, _ := inst.r.Snapshot()
	if !maps.Equal(inst.View(), batchJoin(inst.l.TabSnapshot(), rtab)) {
		return errors.New("api: view != batch recomputation") // 不变量 1
	}
	if !maps.Equal(inst.View(), applied) {
		return errors.New("api: changelog replay != view") // 不变量 2 终态
	}
	return checkFailures() // 不变量 4
}

// applyLog 把日志新增段回放到 applied，校验 -k 存在、+k 值不同。
func applyLog(applied map[string][2]string, log []string, from int) (map[string][2]string, int, error) {
	for _, e := range log[from:] {
		if e[0] == '-' {
			if _, ok := applied[e[1:]]; !ok {
				return nil, 0, fmt.Errorf("api: %s on absent key", e)
			}
			delete(applied, e[1:])
			continue
		}
		eq := strings.IndexByte(e, '=') // 形如 +k=(v,rv)
		if eq < 1 || eq+2 > len(e)-1 || e[eq+1] != '(' || e[len(e)-1] != ')' {
			return nil, 0, fmt.Errorf("api: bad entry %q", e)
		}
		k, body := e[1:eq], e[eq+2:len(e)-1]
		v, rv, _ := strings.Cut(body, ",")
		if cur, ok := applied[k]; ok && cur == [2]string{v, rv} {
			return nil, 0, fmt.Errorf("api: +%s with unchanged value", k)
		}
		applied[k] = [2]string{v, rv}
	}
	return applied, len(log), nil
}

// checkFailures 不变量 4：三类错误可判定、互不相同、被拒后状态不变且可继续用。
func checkFailures() error {
	inst := New(2)
	_ = inst.PutLeft("k1", "A", "x")
	_ = inst.PutLeft("k2", "A", "y")
	snap := fmt.Sprint(inst.View(), inst.Changelog(), inst.Discarded())
	got := []error{inst.PutLeft("", "A", "x"), inst.Deliver("never-seen"),
		inst.PutLeft("k3", "B", "z"), inst.PutRight("A", "a2")}
	want := []error{ErrEmptyKey, ErrEmptyQueue, ErrTooManyPending, ErrTooManyPending}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			return fmt.Errorf("api: fault %d: got %v", i, got[i])
		}
	}
	if ErrEmptyKey == ErrEmptyQueue || ErrEmptyQueue == ErrTooManyPending || ErrEmptyKey == ErrTooManyPending {
		return errors.New("api: sentinel errors not distinct")
	}
	if snap != fmt.Sprint(inst.View(), inst.Changelog(), inst.Discarded()) {
		return errors.New("api: rejected op changed state")
	}
	if inst.DrainAll(); len(inst.View()) != 0 { // 被拒后仍可正常使用：k1/k2 订阅的 A 不存在，结果应为空
		return errors.New("api: unusable after rejection")
	}
	return nil
}
