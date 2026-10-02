package ontology

// shredResult 存放一条记录按列拆出的条目（仅在校验全部通过后提交）。
type shredResult struct {
	cols [][]Entry
}

// Shred 将一条记录原子地拆分进各列。
func (s *Shredder) Shred(rec map[string]any) error {
	res := shredResult{cols: make([][]Entry, len(s.leaves))}
	if err := s.walkGroup(s.indexTree, rec, "", 0, 0, &res); err != nil {
		return err
	}
	for i := range res.cols {
		if len(res.cols[i]) > s.maxEntries {
			return &FieldError{Kind: ErrTooLarge, Path: s.leaves[i].name}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	recIdx := s.recordN
	for i := range res.cols {
		s.commitColumn(i, recIdx, res.cols[i])
	}
	s.recordN++
	return nil
}

// walkGroup 校验并拆分一个分组（值必须为 map），返回值本身保证非 nil。
func (s *Shredder) walkGroup(nodes []schemaNode, group map[string]any, parent string, rep, def int, res *shredResult) error {
	// 先检查未知键（取字节序最小者）。
	known := make(map[string]bool, len(nodes))
	for i := range nodes {
		known[nodes[i].field.Name] = true
	}
	for _, k := range sortedKeys(group) {
		if !known[k] {
			return unknownError(joinPath(parent, k))
		}
	}
	for i := range nodes {
		n := &nodes[i]
		v, present := group[n.field.Name]
		if err := s.walkField(n, v, present, rep, def, res); err != nil {
			return err
		}
	}
	return nil
}

// walkField 按声明次序处理单个字段（调用方已保证该字段在当前分组内的校验位次）。
func (s *Shredder) walkField(n *schemaNode, v any, present bool, repIn, defIn int, res *shredResult) error {
	f := n.field
	path := n.path
	leaf := len(f.Children) == 0

	switch f.Rep {
	case Required:
		if !present || v == nil {
			return missingError(path)
		}
		if leaf {
			x, ok := v.(int64)
			if !ok {
				return typeError(path)
			}
			emitLeaf(res, n.leafLo, repIn, defIn, &x)
			return nil
		}
		g, ok := v.(map[string]any)
		if !ok {
			return typeError(path)
		}
		return s.walkGroup(n.children, g, path, repIn, defIn, res)

	case Optional:
		if !present || v == nil {
			emitNullRange(res, n, repIn, defIn)
			return nil
		}
		if leaf {
			x, ok := v.(int64)
			if !ok {
				return typeError(path)
			}
			emitLeaf(res, n.leafLo, repIn, defIn+1, &x)
			return nil
		}
		g, ok := v.(map[string]any)
		if !ok {
			return typeError(path)
		}
		return s.walkGroup(n.children, g, path, repIn, defIn+1, res)

	case Repeated:
		if !present || v == nil {
			emitNullRange(res, n, repIn, defIn)
			return nil
		}
		list, ok := v.([]any)
		if !ok {
			return typeError(path)
		}
		if len(list) == 0 {
			emitNullRange(res, n, repIn, defIn)
			return nil
		}
		repLevel := n.repLevel
		for j, elem := range list {
			rep := repIn
			if j >= 1 {
				rep = repLevel
			}
			if elem == nil {
				return typeError(path)
			}
			if leaf {
				x, ok := elem.(int64)
				if !ok {
					return typeError(path)
				}
				emitLeaf(res, n.leafLo, rep, defIn+1, &x)
				continue
			}
			g, ok := elem.(map[string]any)
			if !ok {
				return typeError(path)
			}
			if err := s.walkGroup(n.children, g, path, rep, defIn+1, res); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// emitNullRange 向字段子树的所有叶子列写一个空条目。
func emitNullRange(res *shredResult, n *schemaNode, rep, def int) {
	for c := n.leafLo; c < n.leafHi; c++ {
		res.cols[c] = append(res.cols[c], Entry{Rep: rep, Def: def})
	}
}

func emitLeaf(res *shredResult, col, rep, def int, v *int64) {
	e := Entry{Rep: rep, Def: def}
	if v != nil {
		e.Value = *v
	}
	res.cols[col] = append(res.cols[col], e)
}
