package ontology

// Assemble 从各列按记录还原全部记录。
func (s *Shredder) Assemble() ([]map[string]any, error) {
	s.mu.RLock()
	n := s.recordN
	cols := make([][]Entry, len(s.cols))
	total := 0
	for i := range s.cols {
		cols[i] = append(cols[i], s.cols[i].entries...)
		total += len(cols[i])
	}
	s.read = total
	s.mu.RUnlock()

	recs := make([]map[string]any, n)
	for r := range recs {
		recs[r] = map[string]any{}
	}

	pos := make([]int, len(cols))
	for r := 0; r < n; r++ {
		b := newAssembler(recs[r])
		for ci, es := range cols {
			b.startColumn()
			if pos[ci] >= len(es) || es[pos[ci]].Rep != 0 {
				return nil, &FieldError{Kind: ErrInconsistentCols, Path: s.leaves[ci].name}
			}
			for pos[ci] < len(es) {
				e := es[pos[ci]]
				pos[ci]++
				if err := b.apply(e, s.leaves[ci], s.leafPaths[ci]); err != nil {
					return nil, err
				}
				if pos[ci] < len(es) && es[pos[ci]].Rep == 0 {
					break
				}
			}
		}
	}
	return recs, nil
}

type assembler struct {
	root map[string]any
	// idx[level] 是当前列在第 level 个重复层上的当前元素下标（从 0 开始）。
	idx map[int]int
	// fresh[level] 表示该层因外层推进需要在下一次遇到时从元素 0 重新开始。
	fresh map[int]bool
}

func newAssembler(root map[string]any) *assembler {
	return &assembler{root: root, idx: map[int]int{}, fresh: map[int]bool{}}
}

func (b *assembler) startColumn() {
	b.idx = map[int]int{}
	b.fresh = map[int]bool{}
}

func (b *assembler) apply(e Entry, leaf leafInfo, path []*schemaNode) error {
	group := b.root

	// 新条目 rep=R：所有深于 R 的重复层进入新的外层元素，下次落回元素 0。
	for level := 1; level <= 6; level++ {
		if level > e.Rep {
			b.fresh[level] = true
		}
	}

	for _, n := range path {
		if e.Def < n.defLevel {
			return nil
		}
		isLeaf := len(n.field.Children) == 0

		if n.field.Rep == Repeated {
			if isLeaf {
				list, _ := group[n.field.Name].([]any)
				if e.Def == leaf.maxDef {
					list = append(list, e.Value)
				}
				group[n.field.Name] = list
				return nil
			}
			// rep 恰为该层序号：同层新元素（下标 +1）；
			// 该层首次出现或因外层推进标记 fresh：落到元素 0；
			// 否则沿用当前下标。
			if e.Rep == n.repLevel {
				b.idx[n.repLevel]++
				b.fresh[n.repLevel] = false
			} else if _, ok := b.idx[n.repLevel]; !ok || b.fresh[n.repLevel] {
				b.idx[n.repLevel] = 0
				b.fresh[n.repLevel] = false
			}
			ix := b.idx[n.repLevel]
			elem, err := ensureGroupElem(group, n.field.Name, ix)
			if err != nil {
				return &FieldError{Kind: ErrInconsistentCols, Path: n.path}
			}
			group = elem
			continue
		}

		if isLeaf {
			if e.Def == leaf.maxDef {
				group[n.field.Name] = e.Value
			}
			return nil
		}

		if n.field.Rep == Optional || n.field.Rep == Required {
			child, _ := group[n.field.Name].(map[string]any)
			if child == nil {
				child = map[string]any{}
				group[n.field.Name] = child
			}
			group = child
		}
	}
	return nil
}

func ensureGroupElem(group map[string]any, name string, ix int) (map[string]any, error) {
	list, _ := group[name].([]any)
	for len(list) <= ix {
		list = append(list, any(map[string]any{}))
	}
	elem, ok := list[ix].(map[string]any)
	if !ok {
		return nil, ErrInconsistentCols
	}
	group[name] = list
	return elem, nil
}
