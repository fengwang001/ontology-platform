// Package input 按回合缓存提交窗口内的玩家输入。
package input

import "sort"

// Window 缓存未结算回合的已提交输入，并维护每回合“活跃提交者”计数，
// 使到齐判定为 O(1) 而非遍历玩家。窗口内回合数不超过 A+1，与 N 无关。
type Window struct {
	cmds map[int]map[int][]byte // 回合 -> 玩家 -> 输入
	cnt  map[int]int            // 回合 -> 已提交该回合的活跃玩家数
}

// New 返回空的提交窗口。
func New() *Window {
	return &Window{cmds: make(map[int]map[int][]byte), cnt: make(map[int]int)}
}

// Has 报告玩家 p 是否已提交回合 k。
func (w *Window) Has(p, k int) bool {
	_, ok := w.cmds[k][p]
	return ok
}

// Get 返回玩家 p 在回合 k 的输入。
func (w *Window) Get(p, k int) ([]byte, bool) {
	cmd, ok := w.cmds[k][p]
	return cmd, ok
}

// Add 存入玩家 p 对回合 k 的输入（调用方保证参数合法且不重复）。
func (w *Window) Add(p, k int, cmd []byte) {
	m := w.cmds[k]
	if m == nil {
		m = make(map[int][]byte)
		w.cmds[k] = m
	}
	m[p] = cmd
}

// Count 返回已提交回合 k 的活跃玩家数。
func (w *Window) Count(k int) int { return w.cnt[k] }

// Bump 将回合 k 的活跃提交者计数加 delta（活跃标记变化时由外层调用）。
func (w *Window) Bump(k, delta int) {
	v := w.cnt[k] + delta
	if v == 0 {
		delete(w.cnt, k)
		return
	}
	w.cnt[k] = v
}

// Turns 返回玩家 p 已缓存输入的回合号（升序）。结果数不超过窗口大小。
func (w *Window) Turns(p int) []int {
	var ks []int
	for k, m := range w.cmds {
		if _, ok := m[p]; ok {
			ks = append(ks, k)
		}
	}
	sort.Ints(ks)
	return ks
}

// Clear 删除回合 k 的全部缓存（结算后调用）。
func (w *Window) Clear(k int) {
	delete(w.cmds, k)
	delete(w.cnt, k)
}
