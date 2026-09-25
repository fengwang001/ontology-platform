// Package api 是 DRF 分配器的对外接口：New/Add/Allocate/SelfCheck，只依赖 alloc。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/alloc"
	"ontology/drf"
)

// Allocator 是线程安全的两资源 DRF 分配器，状态只在进程内存中。
type Allocator struct {
	mu     sync.RWMutex
	cpuCap int64
	memCap int64
	tasks  []alloc.Task
}

// 五类互异哨兵错误。
var (
	ErrEmptyID     = errors.New("api: empty task id")
	ErrDuplicateID = errors.New("api: duplicate task id")
	ErrNegative    = errors.New("api: negative resource demand")
	ErrZeroDemand  = errors.New("api: both cpu and mem demand are zero")
	ErrBadCapacity = errors.New("api: capacity must be positive")
)

// New 创建容量为 (cpuCap,memCap) 的分配器；容量必须均为正。
func New(cpuCap, memCap int64) (*Allocator, error) {
	if cpuCap <= 0 || memCap <= 0 {
		return nil, ErrBadCapacity
	}
	return &Allocator{cpuCap: cpuCap, memCap: memCap}, nil
}

// Add 加入一个任务；任何校验失败都整体拒绝且不改变任何状态（先校验后落状态）。
func (a *Allocator) Add(id string, cpu, mem int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case id == "":
		return ErrEmptyID
	case cpu < 0 || mem < 0:
		return ErrNegative
	case cpu == 0 && mem == 0:
		return ErrZeroDemand
	}
	for _, t := range a.tasks {
		if t.ID == id {
			return ErrDuplicateID
		}
	}
	a.tasks = append(a.tasks, alloc.Task{ID: id, CPU: cpu, Mem: mem})
	return nil
}

func intFrac(v int64) drf.Frac { return drf.Frac{N: v, D: 1} }

// Allocate 返回 id → 精确分配单位数；在任务快照上计算，可与 Add 并发。
func (a *Allocator) Allocate() map[string]drf.Frac {
	a.mu.RLock()
	snap := append([]alloc.Task(nil), a.tasks...)
	cc, cm := a.cpuCap, a.memCap
	a.mu.RUnlock()
	al := alloc.New(cc, cm)
	for _, t := range snap {
		al.Add(t)
	}
	return al.Allocate()
}

// SelfCheck 对内置任务组（容量 60/60，A(1,6)、B(4,1)）逐条核验四条不变量。
func (a *Allocator) SelfCheck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	const cc, cm int64 = 60, 60
	tasks := []alloc.Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}}
	al := alloc.New(cc, cm)
	for _, t := range tasks {
		al.Add(t)
	}
	got := al.Allocate()
	want := alloc.NaiveAllocate(cc, cm, tasks) // I1：与朴素参照逐 id 相同
	if len(got) != len(want) {
		return fmt.Errorf("selfcheck I1: id set mismatch")
	}
	var useC, useM, minS, maxS drf.Frac
	for i, t := range tasks {
		v, ok := want[t.ID]
		if !ok || drf.Cmp(got[t.ID], v) != 0 {
			return fmt.Errorf("selfcheck I1: %s differs from naive", t.ID)
		}
		useC = drf.Add(useC, drf.Mul(got[t.ID], intFrac(t.CPU)))
		useM = drf.Add(useM, drf.Mul(got[t.ID], intFrac(t.Mem)))
		s := drf.DominantShare(got[t.ID], t.CPU, t.Mem, cc, cm)
		if i == 0 {
			minS, maxS = s, s
		}
		if drf.Cmp(s, minS) < 0 {
			minS = s
		}
		if drf.Cmp(s, maxS) > 0 {
			maxS = s
		}
	}
	if drf.Cmp(useC, intFrac(cc)) > 0 || drf.Cmp(useM, intFrac(cm)) > 0 ||
		(drf.Cmp(useC, intFrac(cc)) < 0 && drf.Cmp(useM, intFrac(cm)) < 0) { // I2
		return fmt.Errorf("selfcheck I2: hard constraints violated: cpu=%v mem=%v", useC, useM)
	}
	if drf.Cmp(minS, maxS) != 0 { // I3：内置组主导份额必须全部相等
		return fmt.Errorf("selfcheck I3: dominant shares not equalized")
	}
	if err := checkRejectedLeavesNoTrace(); err != nil { // I4
		return err
	}
	return nil
}

// checkRejectedLeavesNoTrace 用内置组验证五类拒绝互不相同且状态不留痕。
func checkRejectedLeavesNoTrace() error {
	a, _ := New(60, 60)
	for _, t := range []struct {
		id       string
		cpu, mem int64
		want     error
	}{
		{"", 1, 1, ErrEmptyID},
		{"A", 1, 6, nil},
		{"A", 2, 2, ErrDuplicateID},
		{"B", -1, 1, ErrNegative},
		{"C", 0, 0, ErrZeroDemand},
	} {
		if err := a.Add(t.id, t.cpu, t.mem); !errors.Is(err, t.want) {
			return fmt.Errorf("selfcheck I4: Add(%q) err=%v want %v", t.id, err, t.want)
		}
	}
	if len(a.tasks) != 1 {
		return fmt.Errorf("selfcheck I4: %d tasks survived rejects, want 1", len(a.tasks))
	}
	if _, err := New(0, 60); !errors.Is(err, ErrBadCapacity) {
		return fmt.Errorf("selfcheck I4: New(0,60) err=%v want ErrBadCapacity", err)
	}
	return nil
}
