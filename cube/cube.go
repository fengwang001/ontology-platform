// Package cube 按键维护求和：Add/Remove 的 8-掩码遍历、
// maxCells 上限判定、求和为 0 即删键。依赖 dim，不依赖 api。
package cube

import (
	"errors"
	"sort"
	"sync"

	"ontology/dim"
)

var (
	// ErrCellLimit：Add 后非空 cell 数将超过 maxCells，整体拒绝。
	ErrCellLimit = errors.New("cube: cell count would exceed maxCells")
	// ErrFactNotFound：Remove 一个当前求和记录中不存在的事实。
	ErrFactNotFound = errors.New("cube: fact not present")
)

type factKey struct {
	a, b, c string
	v       int64
}

// Entry 是一个非空 cell 的键与求和。
type Entry struct {
	Key dim.Key
	Sum int64
}

// Cube 是三维 CUBE 的增量物化视图，goroutine 安全。
type Cube struct {
	mu      sync.RWMutex
	sums    map[dim.Key]int64
	facts   map[factKey]int64 // 事实净次数，用于 Remove 存在性判定
	max     int
	touched int // 非导出：最近一次 Add/Remove 触碰的 cell 键个数
}

// New 构造上限为 maxCells 的 Cube（maxCells 已由调用方校验为正）。
func New(maxCells int) *Cube {
	return &Cube{sums: map[dim.Key]int64{}, facts: map[factKey]int64{}, max: maxCells}
}

// apply 对事实的 8 个掩码 cell 统一加 delta。checkLimit 为 true 时
// 先全量模拟，超限则整体拒绝、不留痕。求和变 0 的 cell 立即删除。
func (c *Cube) apply(a, b, c2 string, v int64, checkLimit bool) error {
	keys := dim.Cells(a, b, c2)
	if checkLimit {
		n := len(c.sums)
		for _, k := range keys {
			old, ok := c.sums[k]
			switch {
			case !ok && old+v != 0:
				n++
			case ok && old+v == 0:
				n--
			}
		}
		if n > c.max {
			return ErrCellLimit
		}
	}
	for _, k := range keys {
		s := c.sums[k] + v
		if s == 0 {
			delete(c.sums, k)
		} else {
			c.sums[k] = s
		}
	}
	c.touched = len(keys)
	return nil
}

// Add 把事实累加进 8 个 cell；超 maxCells 整体拒绝。
func (c *Cube) Add(a, b, c2 string, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.apply(a, b, c2, v, true); err != nil {
		return err
	}
	c.facts[factKey{a, b, c2, v}]++
	return nil
}

// Remove 把事实从 8 个 cell 减去；事实不存在则整体拒绝。
func (c *Cube) Remove(a, b, c2 string, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fk := factKey{a, b, c2, v}
	if c.facts[fk] == 0 {
		return ErrFactNotFound
	}
	if err := c.apply(a, b, c2, -v, false); err != nil {
		return err
	}
	if c.facts[fk]--; c.facts[fk] == 0 {
		delete(c.facts, fk)
	}
	return nil
}

// View 返回所有非空 cell（求和不为 0），按键确定性排序。
func (c *Cube) View() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Entry, 0, len(c.sums))
	for k, s := range c.sums {
		out = append(out, Entry{Key: k, Sum: s})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Key, out[j].Key
		for d := 0; d < 3; d++ {
			if a.Conc[d] != b.Conc[d] {
				return !a.Conc[d] // ALL 排在具体值前
			}
			if a.V[d] != b.V[d] {
				return a.V[d] < b.V[d]
			}
		}
		return false
	})
	return out
}
