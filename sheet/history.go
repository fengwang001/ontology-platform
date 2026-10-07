package sheet

// userHistory 是单个用户的撤销栈与重做栈。
// 栈顶位于切片末尾。撤销栈深度受 D 限制；重做栈只能由 Undo 压入、
// 被 Apply 清空，因此其大小天然不超过 D，无需额外限制。
type userHistory struct {
	undo []Record
	redo []Record
}

// pushUndo 压入撤销栈；超过 depth 时丢弃最旧的一条。
// depth 上限为 100（MaxDepth），移动开销为常数级。
func (h *userHistory) pushUndo(r Record, depth int) {
	h.undo = append(h.undo, r)
	if len(h.undo) > depth {
		h.undo = append(h.undo[:0], h.undo[1:]...)
	}
}

func (h *userHistory) popUndo() (Record, bool) {
	if len(h.undo) == 0 {
		return Record{}, false
	}
	top := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	return top, true
}

func (h *userHistory) peekUndo() (Record, bool) {
	if len(h.undo) == 0 {
		return Record{}, false
	}
	return h.undo[len(h.undo)-1], true
}

func (h *userHistory) pushRedo(r Record) {
	h.redo = append(h.redo, r)
}

func (h *userHistory) popRedo() (Record, bool) {
	if len(h.redo) == 0 {
		return Record{}, false
	}
	top := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	return top, true
}

func (h *userHistory) peekRedo() (Record, bool) {
	if len(h.redo) == 0 {
		return Record{}, false
	}
	return h.redo[len(h.redo)-1], true
}

func (h *userHistory) clearRedo() {
	h.redo = nil
}
