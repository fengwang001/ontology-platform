package lifecycle

// workView 是一个处理单元内的私有工作视图：以存储快照为基底，
// 用稀疏 overlay 累积本单元已接受操作造成的属性/状态/链接变化。
// 规划阶段的所有求值（前置条件、迁移后基数、钩子、级联对端发现）
// 都在该视图上完成，因此求值只访问当前实例及其关联实例，
// 从不遍历历史迁移记录。
type workView struct {
	base *snap

	state map[string]string
	attrs map[string]map[string]AttrValue

	// out 维护每个实例当前的出向邻接（基底 + 本单元增删），
	// 使基数与对端发现的开销只随涉及的链接数量增长。
	out map[string]map[string]map[string]bool

	// dirtyAdj 记录本单元中邻接被增删过的属主。
	dirtyAdj map[string]bool
}

func newWorkView(base *snap) *workView {
	w := &workView{
		base:     base,
		state:    map[string]string{},
		attrs:    map[string]map[string]AttrValue{},
		out:      map[string]map[string]map[string]bool{},
		dirtyAdj: map[string]bool{},
	}
	for _, in := range base.inst {
		for typ, set := range in.out {
			for to := range set {
				w.putAdj(in.ID, typ, to)
			}
		}
	}
	return w
}

func (w *workView) instance(id string) *Instance {
	in, ok := w.base.inst[id]
	if !ok {
		return nil
	}
	c := *in
	if st, ok := w.state[id]; ok {
		c.State = st
	}
	if ov, ok := w.attrs[id]; ok {
		c.Attrs = cloneAttrs(in.Attrs)
		for k, v := range ov {
			if v == nil {
				delete(c.Attrs, k)
			} else {
				c.Attrs[k] = v
			}
		}
	}
	return &c
}

func (w *workView) stateOf(id string) (string, bool) {
	if st, ok := w.state[id]; ok {
		return st, true
	}
	in, ok := w.base.inst[id]
	if !ok {
		return "", false
	}
	return in.State, true
}

func (w *workView) attr(id, name string) (AttrValue, bool) {
	if ov, ok := w.attrs[id]; ok {
		if v, ok := ov[name]; ok {
			if v == nil {
				return nil, false
			}
			return v, true
		}
	}
	in, ok := w.base.inst[id]
	if !ok {
		return nil, false
	}
	v, ok := in.Attrs[name]
	return v, ok
}

func (w *workView) setAttr(id, name string, v AttrValue) {
	ov := w.attrs[id]
	if ov == nil {
		ov = map[string]AttrValue{}
		w.attrs[id] = ov
	}
	ov[name] = v
}

func (w *workView) setState(id, st string) {
	w.state[id] = st
}

func (w *workView) hasLink(l Link) bool {
	if tm := w.out[l.FromID]; tm != nil {
		if set := tm[l.Type]; set != nil {
			return set[l.ToID]
		}
	}
	return false
}

func (w *workView) addLink(l Link) bool {
	if w.hasLink(l) {
		return false
	}
	w.putAdj(l.FromID, l.Type, l.ToID)
	w.dirtyAdj[l.FromID] = true
	return true
}

func (w *workView) delLink(l Link) bool {
	if !w.hasLink(l) {
		return false
	}
	w.delAdj(l.FromID, l.Type, l.ToID)
	w.dirtyAdj[l.FromID] = true
	return true
}

// peers 返回实例经 linkType 连接的全部对端 ID（快照顺序无关，排序由调用方处理）。
func (w *workView) peers(id, linkType string) []string {
	tm, ok := w.out[id]
	if !ok {
		return nil
	}
	set := tm[linkType]
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sortStrings(out)
	return out
}

func (w *workView) linkCount(id, linkType string) int {
	tm, ok := w.out[id]
	if !ok {
		return 0
	}
	return len(tm[linkType])
}

func (w *workView) putAdj(from, typ, to string) {
	tm := w.out[from]
	if tm == nil {
		tm = map[string]map[string]bool{}
		w.out[from] = tm
	}
	set := tm[typ]
	if set == nil {
		set = map[string]bool{}
		tm[typ] = set
	}
	set[to] = true
}

func (w *workView) delAdj(from, typ, to string) {
	if tm := w.out[from]; tm != nil {
		if set := tm[typ]; set != nil {
			delete(set, to)
		}
	}
}

// touchedInstances 返回本单元中状态/属性/时钟被改动过的实例。
