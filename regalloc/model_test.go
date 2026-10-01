package regalloc

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveInterval is one interval tracked by the naive simulation.
type naiveInterval struct {
	id    int
	start int
	end   int
	reg   int // register held while live
}

// naiveModel is a deliberately simple, step-by-step simulation written
// directly from the allocation rules. Every step logs the input, the
// output, and the rule that produced it, so `go test -v` yields an
// auditable trace.
type naiveModel struct {
	k        int
	regFree  []bool
	live     []*naiveInterval // active intervals in arrival order
	asg      map[int]Assignment
	nextSlot int
	last     int
	hasLast  bool
	logf     func(format string, args ...any)
}

func newNaiveModel(k int, logf func(format string, args ...any)) *naiveModel {
	regFree := make([]bool, k)
	for r := range regFree {
		regFree[r] = true
	}
	return &naiveModel{
		k:       k,
		regFree: regFree,
		asg:     make(map[int]Assignment),
		logf:    logf,
	}
}

// add mirrors the documented rules one step at a time.
func (m *naiveModel) add(id, start, end int) (Assignment, error) {
	m.logf("输入: Add(id=%d, [%d,%d))", id, start, end)

	if _, dup := m.asg[id]; dup {
		m.logf("判定: 编号 %d 已存在 -> 拒绝（ErrDuplicateID）", id)
		return Assignment{}, fmt.Errorf("%w: id=%d", ErrDuplicateID, id)
	}
	if start >= end {
		m.logf("判定: 起点 %d 不小于终点 %d -> 拒绝（ErrInvalidRange）", start, end)
		return Assignment{}, fmt.Errorf("%w: id=%d", ErrInvalidRange, id)
	}
	if m.hasLast && start < m.last {
		m.logf("判定: 起点 %d 小于上一成功起点 %d -> 拒绝（ErrStartRegression）", start, m.last)
		return Assignment{}, fmt.Errorf("%w: id=%d", ErrStartRegression, id)
	}

	// 释放: 终点 <= 新起点的活跃区间全部到期，终点恰等于新起点也算已释放。
	still := m.live[:0]
	for _, iv := range m.live {
		if iv.end <= start {
			m.logf("判定: 区间 %d 终点 %d <= 新起点 %d，到期释放寄存器 %d", iv.id, iv.end, start, iv.reg)
			m.regFree[iv.reg] = true
		} else {
			still = append(still, iv)
		}
	}
	m.live = still

	iv := &naiveInterval{id: id, start: start, end: end, reg: -1}
	var asg Assignment

	reg := -1
	for r := 0; r < m.k; r++ {
		if m.regFree[r] {
			reg = r
			break
		}
	}

	if reg >= 0 {
		m.logf("判定: 有空闲寄存器，取最小编号 %d 分配给区间 %d", reg, id)
		m.regFree[reg] = false
		iv.reg = reg
		m.live = append(m.live, iv)
		asg = Assignment{Register: reg}
	} else {
		// 溢出候选: 活跃区间中终点最大者，并列取编号最小者。
		var victim *naiveInterval
		for _, c := range m.live {
			if victim == nil || c.end > victim.end || (c.end == victim.end && c.id < victim.id) {
				victim = c
			}
		}
		if victim == nil || end >= victim.end {
			m.logf("判定: 无空闲寄存器，新区间终点 %d 不小于活跃最大终点（并列新区间优先），区间 %d 自溢出 -> 槽 %d",
				end, id, m.nextSlot)
			asg = Assignment{Spilled: true, Slot: m.nextSlot}
			m.nextSlot++
		} else {
			m.logf("判定: 无空闲寄存器，活跃区间 %d 终点 %d 最大（并列取最小编号），溢出 -> 槽 %d，寄存器 %d 立即转给区间 %d",
				victim.id, victim.end, m.nextSlot, victim.reg, id)
			m.asg[victim.id] = Assignment{Spilled: true, Slot: m.nextSlot}
			m.nextSlot++
			kept := m.live[:0]
			for _, c := range m.live {
				if c != victim {
					kept = append(kept, c)
				}
			}
			m.live = kept
			iv.reg = victim.reg
			m.live = append(m.live, iv)
			asg = Assignment{Register: iv.reg}
		}
	}

	m.asg[id] = asg
	m.last = start
	m.hasLast = true
	m.logf("输出: 区间 %d -> %+v", id, asg)
	return asg, nil
}

// checkInvariants verifies, under the allocator lock, that no register is
// held by two live intervals and that spill slots are contiguous from 0.
func checkInvariants(t *testing.T, a *Allocator) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()

	owner := make(map[int]int)
	for id, iv := range a.active {
		if iv.spilled {
			t.Fatalf("不变量违反: 活跃区间 %d 被标记为已溢出", id)
		}
		if prev, ok := owner[iv.reg]; ok {
			t.Fatalf("不变量违反: 寄存器 %d 同时被活跃区间 %d 与 %d 占用", iv.reg, prev, id)
		}
		owner[iv.reg] = id
		if a.free[iv.reg] {
			t.Fatalf("不变量违反: 寄存器 %d 既空闲又被区间 %d 占用", iv.reg, id)
		}
	}

	slots := make(map[int]int)
	for id, iv := range a.intervals {
		if !iv.spilled {
			continue
		}
		if prev, dup := slots[iv.slot]; dup {
			t.Fatalf("不变量违反: 溢出槽 %d 被区间 %d 与 %d 复用", iv.slot, prev, id)
		}
		slots[iv.slot] = id
	}
	for s := 0; s < a.nextSlot; s++ {
		if _, ok := slots[s]; !ok {
			t.Fatalf("不变量违反: 溢出槽 %d 空洞（共 %d 个槽）", s, a.nextSlot)
		}
	}
}

// TestAgainstNaiveModel replays randomized non-decreasing interval
// sequences through both the allocator and the naive simulation and
// requires identical outcomes, logging inputs, outputs, and rationale.
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 20; trial++ {
		k := 1 + rng.Intn(4)
		a := mustNew(t, k)
		m := newNaiveModel(k, t.Logf)
		t.Logf("=== 试验 %d: K=%d ===", trial, k)

		const n = 30
		start := 0
		for i := 0; i < n; i++ {
			start += rng.Intn(4) // 起点非降序
			length := 1 + rng.Intn(10)
			id := trial*1000 + i

			want, wantErr := m.add(id, start, start+length)
			got, gotErr := a.Add(id, start, start+length)
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("Add(%d, %d, %d): 模型错误 %v, 实现错误 %v", id, start, start+length, wantErr, gotErr)
			}
			if wantErr == nil && want != got {
				t.Fatalf("Add(%d, %d, %d): 模型 %+v, 实现 %+v", id, start, start+length, want, got)
			}
			checkInvariants(t, a)
		}

		for i := 0; i < n; i++ {
			id := trial*1000 + i
			got := mustQuery(t, a, id)
			if want := m.asg[id]; got != want {
				t.Fatalf("Query(%d): 模型 %+v, 实现 %+v", id, want, got)
			}
		}
	}
}

// TestConcurrentAddQuery hammers Add and Query from many goroutines. All
// intervals share one start point, so every serialization of the calls is
// a legal input order; the result must satisfy the allocator invariants
// and leave exactly K register holders (all intervals overlap).
func TestConcurrentAddQuery(t *testing.T) {
	const (
		k       = 3
		workers = 8
		per     = 25
	)
	a := mustNew(t, k)

	var wg sync.WaitGroup
	errs := make(chan error, workers*per*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := w*per + i
				if _, err := a.Add(id, 10, 11+(id%7)); err != nil {
					errs <- fmt.Errorf("Add(%d): %w", id, err)
					return
				}
				if _, err := a.Query(id); err != nil {
					errs <- fmt.Errorf("Query(%d): %w", id, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	checkInvariants(t, a)

	holders, spilled := 0, 0
	for id := 0; id < workers*per; id++ {
		asg := mustQuery(t, a, id)
		if asg.Spilled {
			spilled++
		} else {
			holders++
		}
	}
	if holders != k || spilled != workers*per-k {
		t.Fatalf("并发结果: %d 个持寄存器（期望 %d），%d 个溢出（期望 %d）",
			holders, k, spilled, workers*per-k)
	}
	t.Logf("并发完成: %d 个区间, %d 个持寄存器, %d 个溢出, 不变量通过", workers*per, holders, spilled)
}
