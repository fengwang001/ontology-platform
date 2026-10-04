package input

// Entry 是某玩家在某回合窗口内的一份提前提交输入。
type Entry struct {
	Cmd []byte
}

// Window 是以回合为键的稀疏提交窗口。
// ready[k] 只统计“已提交且活跃”的玩家数，使到齐判定与 N 无关。
type Window struct {
	cur     int
	ahead   int
	turns   map[int]map[int]*Entry
	ready   map[int]int
	touched int
}

func New(n, cur, ahead int) *Window {
	return &Window{
		cur:   cur,
		ahead: ahead,
		turns: make(map[int]map[int]*Entry),
		ready: make(map[int]int),
	}
}

// Put 存入 (k,p) 的输入（以首次为准，重复由调用方先 Has 判定）。
func (w *Window) Put(k, p int, cmd []byte) {
	slots := w.turns[k]
	if slots == nil {
		slots = make(map[int]*Entry)
		w.turns[k] = slots
	}
	stored := make([]byte, len(cmd))
	copy(stored, cmd)
	slots[p] = &Entry{Cmd: stored}
	w.touched++
}

func (w *Window) Has(k, p int) bool {
	if slots := w.turns[k]; slots != nil {
		_, ok := slots[p]
		return ok
	}
	return false
}

// Ready 返回回合 k 已提交的活跃玩家数。
func (w *Window) Ready(k int) int {
	return w.ready[k]
}

// CountSubmitted 在“当前活跃玩家”新存入一份输入后，递增该回合就绪计数。
func (w *Window) CountSubmitted(k int) {
	w.ready[k]++
}

// Take 取出回合 k 全部槽位并丢弃该回合数据。
func (w *Window) Take(k int) map[int]*Entry {
	slots := w.turns[k]
	delete(w.turns, k)
	delete(w.ready, k)
	w.cur = k + 1
	return slots
}

// StoredFor 返回玩家 p 在窗口内已存的全部回合号。
func (w *Window) StoredFor(p int) []int {
	ks := make([]int, 0, len(w.turns))
	for k, slots := range w.turns {
		if _, ok := slots[p]; ok {
			ks = append(ks, k)
		}
	}
	return ks
}

// Remove 丢弃 (k,p) 槽位；counted 表示该槽位当前是否计入就绪计数（玩家活跃）。
func (w *Window) Remove(k, p int, counted bool) {
	slots := w.turns[k]
	if slots == nil {
		return
	}
	if _, ok := slots[p]; !ok {
		return
	}
	delete(slots, p)
	if len(slots) == 0 {
		delete(w.turns, k)
	}
	if counted {
		w.ready[k]--
		if w.ready[k] <= 0 {
			delete(w.ready, k)
		}
	}
	w.touched++
}

// MarkActive 在玩家恢复活跃时，把其窗口内已存槽位计入各回合就绪数。
func (w *Window) MarkActive(p int) {
	for k, slots := range w.turns {
		if _, ok := slots[p]; ok {
			w.ready[k]++
			w.touched++
		}
	}
}

// Deactivate 在玩家掉线时把其已存槽位从各回合就绪计数中剔除，但保留槽位本身，
// 使该输入在未来回合结算时仍可按实交处理。
func (w *Window) Deactivate(p int) {
	for k, slots := range w.turns {
		if _, ok := slots[p]; ok {
			w.ready[k]--
			if w.ready[k] <= 0 {
				delete(w.ready, k)
			}
			w.touched++
		}
	}
}

// Touched 是自上次 ResetTouched 以来触碰的玩家槽位数（非导出计数器的观测口）。
func (w *Window) Touched() int {
	return w.touched
}

func (w *Window) ResetTouched() {
	w.touched = 0
}
