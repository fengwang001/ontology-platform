package wal

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want error %v, got %v", want, err)
	}
}

// norm 把 nil 与空切片统一，便于比较。
func norm(s []uint64) []uint64 {
	if s == nil {
		return []uint64{}
	}
	return s
}

func mustObsolete(t *testing.T, m *Manager, want ...uint64) {
	t.Helper()
	got := norm(m.Obsolete())
	if !reflect.DeepEqual(got, norm(want)) {
		t.Fatalf("Obsolete() = %v, want %v", got, norm(want))
	}
}

// w 恰等于某表 first 时不可回收（严格小于才可回收）。
func TestFirstBoundaryNotRecyclable(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"))
	mustOK(t, m.Write("a")) // first = 1
	m.Roll()                // cur = 2，候选 w=1 恰等于 first=1
	mustObsolete(t, m)
	m.Roll() // cur = 3，候选 w=1,2 仍被 first=1 阻挡
	mustObsolete(t, m)
}

// 当前日志 cur 永不可回收；无任何未落盘表时可回收前缀为 [1, cur-1]。
func TestCurrentLogNeverRecyclable(t *testing.T) {
	m := NewManager()
	mustObsolete(t, m) // cur=1 时没有可回收编号
	m.Roll()
	mustObsolete(t, m, 1)
	m.Roll()
	m.Roll()
	mustObsolete(t, m, 1, 2, 3) // cur=4 不在其中
}

// 冻结表在 FlushDone 之前仍阻碍回收。
func TestFrozenBlocksUntilFlushDone(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"))
	mustOK(t, m.Write("a")) // first = 1
	mustOK(t, m.FlushStart("a"))
	m.Roll()
	m.Roll()           // cur = 3
	mustObsolete(t, m) // 冻结表 first=1 仍在阻碍
	mustOK(t, m.FlushDone("a"))
	mustObsolete(t, m, 1, 2)
}

// 冻结后新建的活跃表在下一次写入时才取当时的 cur 作为 first。
func TestNewActiveFirstTakenAtNextWrite(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"))
	mustOK(t, m.Write("a")) // first = 1
	mustOK(t, m.FlushStart("a"))
	m.Roll()
	m.Roll()
	m.Roll()                // cur = 4
	mustObsolete(t, m)      // 旧冻结表 first=1 阻碍
	mustOK(t, m.Write("a")) // 新活跃表 first = 4（取写入时的 cur）
	mustOK(t, m.FlushDone("a"))
	mustObsolete(t, m, 1, 2, 3) // 只受新活跃表 first=4 限制
	m.Roll()                    // cur = 5，w=4 恰等于 first=4
	mustObsolete(t, m, 1, 2, 3)
}

// 长期不写的列族，其 first 一直阻碍回收，直到落盘或 Drop。
func TestIdleCFBlocksRecycling(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("idle"))
	mustOK(t, m.CreateCF("busy"))
	mustOK(t, m.Write("idle")) // first = 1，之后长期不写
	for i := 0; i < 5; i++ {
		mustOK(t, m.Write("busy"))
		mustOK(t, m.FlushStart("busy"))
		mustOK(t, m.FlushDone("busy"))
		m.Roll()
	}
	mustObsolete(t, m) // idle 的 first=1 阻挡一切
	mustOK(t, m.FlushStart("idle"))
	mustOK(t, m.FlushDone("idle"))
	mustObsolete(t, m, 1, 2, 3, 4, 5)
}

// DropCF 解除阻碍，且该名可重新创建为全新列族。
func TestDropCFUnblocksAndRecreate(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"))
	mustOK(t, m.Write("a")) // first = 1
	mustOK(t, m.FlushStart("a"))
	mustOK(t, m.Write("a")) // 新活跃表 first = 1
	m.Roll()
	m.Roll() // cur = 3
	mustObsolete(t, m)
	mustOK(t, m.DropCF("a")) // 全部内存表作废
	mustObsolete(t, m, 1, 2)
	mustOK(t, m.CreateCF("a")) // 重新创建为全新列族
	mustOK(t, m.Write("a"))    // first = 3（全新表取当前 cur）
	mustObsolete(t, m, 1, 2)   // w=3 恰等于新 first，不可回收
}

// Purge 之后 Obsolete 不重复返回已回收编号。
func TestPurgeNotRepeated(t *testing.T) {
	m := NewManager()
	m.Roll()
	m.Roll()
	m.Roll() // cur = 4
	got := m.Purge()
	if want := []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Purge() = %v, want %v", got, want)
	}
	mustObsolete(t, m) // 不重复
	if got := m.Purge(); len(got) != 0 {
		t.Fatalf("second Purge() = %v, want empty", got)
	}
	m.Roll() // cur = 5，只有新编号 4 可回收
	mustObsolete(t, m, 4)
}

// 各类拒绝原因可区分，同一调用按序只报第一个，且被拒绝的操作不改变状态。
func TestRejections(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"))
	mustOK(t, m.Write("a")) // first = 1
	m.Roll()                // cur = 2
	before := m.Obsolete()

	mustErr(t, m.CreateCF(""), ErrEmptyCFName)
	mustErr(t, m.DropCF(""), ErrEmptyCFName)
	mustErr(t, m.Write(""), ErrEmptyCFName)
	mustErr(t, m.FlushStart(""), ErrEmptyCFName)
	mustErr(t, m.FlushDone(""), ErrEmptyCFName)

	mustErr(t, m.CreateCF("a"), ErrCFAlreadyExists)

	mustErr(t, m.DropCF("ghost"), ErrCFNotFound)
	mustErr(t, m.Write("ghost"), ErrCFNotFound)
	mustErr(t, m.FlushStart("ghost"), ErrCFNotFound)
	mustErr(t, m.FlushDone("ghost"), ErrCFNotFound)

	mustOK(t, m.CreateCF("b"))
	mustErr(t, m.FlushStart("b"), ErrActiveMemtableEmpty) // 活跃表为空
	mustErr(t, m.FlushDone("a"), ErrNoFrozenMemtable)     // 冻结队列为空

	// 被拒绝的操作不得改变任何状态。
	if got := m.Obsolete(); !reflect.DeepEqual(norm(got), norm(before)) {
		t.Fatalf("state changed by rejected ops: Obsolete %v -> %v", before, got)
	}
	mustOK(t, m.FlushStart("a"))
	mustOK(t, m.FlushDone("a"))
	mustObsolete(t, m, 1)
}

// 并发调用等价于某个串行顺序：安全性不变量始终成立，
// 即已回收编号不含任何仍被未落盘表依赖的日志。
func TestConcurrentSafety(t *testing.T) {
	m := NewManager()
	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + id))
			if err := m.CreateCF(name); err != nil {
				t.Errorf("CreateCF: %v", err)
				return
			}
			for j := 0; j < 200; j++ {
				_ = m.Write(name)
				m.Roll()
				if j%3 == 0 {
					_ = m.FlushStart(name)
				}
				if j%5 == 0 {
					_ = m.FlushDone(name)
				}
				_ = m.Obsolete()
				_ = m.Purge()
			}
		}(i)
	}
	wg.Wait()

	// 终态不变量：Obsolete 结果全部 < cur 且严格小于最小 first。
	m.mu.Lock()
	defer m.mu.Unlock()
	hi := m.recyclableLocked()
	for w := m.purgedUpTo + 1; w <= hi; w++ {
		if w >= m.cur {
			t.Fatalf("recyclable %d >= cur %d", w, m.cur)
		}
		if min, ok := m.firsts.min(); ok && w >= min {
			t.Fatalf("recyclable %d >= minFirst %d", w, min)
		}
	}
}

// 相同调用序列重放得到完全相同的 Obsolete 与 Purge 结果。
func TestDeterministicReplay(t *testing.T) {
	run := func() (obsolete, purged [][]uint64) {
		m := NewManager()
		_ = m.CreateCF("x")
		_ = m.CreateCF("y")
		_ = m.Write("x")
		m.Roll()
		_ = m.Write("y")
		_ = m.FlushStart("x")
		m.Roll()
		obsolete = append(obsolete, norm(m.Obsolete()))
		purged = append(purged, norm(m.Purge()))
		_ = m.FlushDone("x")
		_ = m.DropCF("y")
		m.Roll()
		obsolete = append(obsolete, norm(m.Obsolete()))
		purged = append(purged, norm(m.Purge()))
		return obsolete, purged
	}
	o1, p1 := run()
	o2, p2 := run()
	if !reflect.DeepEqual(o1, o2) || !reflect.DeepEqual(p1, p2) {
		t.Fatalf("replay mismatch: (%v,%v) vs (%v,%v)", o1, p1, o2, p2)
	}
}
