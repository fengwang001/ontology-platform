package ontology

// naiveNormalize 按题目规则独立地规范化一条合法记录。
func naiveNormalize(fields []Field, rec map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		v, ok := rec[f.Name]
		if !ok || v == nil {
			if f.Rep == Required && len(f.Children) > 0 {
				out[f.Name] = normalizeGroup(f.Children, v.(map[string]any))
			}
			continue
		}
		if len(f.Children) == 0 {
			if f.Rep == Repeated {
				list := v.([]any)
				if len(list) == 0 {
					continue
				}
				cp := make([]any, len(list))
				copy(cp, list)
				out[f.Name] = cp
			} else {
				out[f.Name] = v
			}
			continue
		}
		if f.Rep == Repeated {
			list := v.([]any)
			if len(list) == 0 {
				continue
			}
			cp := make([]any, 0, len(list))
			for _, e := range list {
				cp = append(cp, normalizeGroup(f.Children, e.(map[string]any)))
			}
			out[f.Name] = cp
		} else {
			out[f.Name] = normalizeGroup(f.Children, v.(map[string]any))
		}
	}
	return out
}

func normalizeGroup(fields []Field, g map[string]any) map[string]any {
	return naiveNormalize(fields, g)
}

// naiveShred 独立按题意递归生成每个叶子列的条目。
func naiveShred(fields []Field, rec map[string]any) map[string][]Entry {
	res := map[string][]Entry{}
	var walk func(fs []Field, group map[string]any, parent string, rep, def, repSoFar int)
	walk = func(fs []Field, group map[string]any, parent string, rep, def, repSoFar int) {
		for _, f := range fs {
			path := f.Name
			if parent != "" {
				path = parent + "." + f.Name
			}
			leaf := len(f.Children) == 0
			level := repSoFar
			childDef := def
			if f.Rep == Repeated {
				level++
				childDef++
			}
			if f.Rep == Optional {
				childDef++
			}
			emit := func(r, d int, val *int64) {
				e := Entry{Rep: r, Def: d}
				if val != nil {
					e.Value = *val
				}
				res[path] = append(res[path], e)
			}
			nullSubtree := func(r, d int) {
				var nulls func(fs2 []Field, p string)
				nulls = func(fs2 []Field, p string) {
					for _, g2 := range fs2 {
						np := g2.Name
						if p != "" {
							np = p + "." + g2.Name
						}
						if len(g2.Children) == 0 {
							res[np] = append(res[np], Entry{Rep: r, Def: d})
						} else {
							nulls(g2.Children, np)
						}
					}
				}
				nulls([]Field{f}, parent)
			}

			v, present := group[f.Name]
			switch f.Rep {
			case Required:
				if leaf {
					x := v.(int64)
					emit(rep, def, &x)
				} else {
					walk(f.Children, v.(map[string]any), path, rep, def, repSoFar)
				}
			case Optional:
				if !present || v == nil {
					nullSubtree(rep, def)
					continue
				}
				if leaf {
					x := v.(int64)
					emit(rep, childDef, &x)
				} else {
					walk(f.Children, v.(map[string]any), path, rep, childDef, repSoFar)
				}
			case Repeated:
				if !present || v == nil {
					nullSubtree(rep, def)
					continue
				}
				list := v.([]any)
				if len(list) == 0 {
					nullSubtree(rep, def)
					continue
				}
				for j, elem := range list {
					r := rep
					if j >= 1 {
						r = level
					}
					if leaf {
						x := elem.(int64)
						emit(r, childDef, &x)
					} else {
						walk(f.Children, elem.(map[string]any), path, r, childDef, level)
					}
				}
			}
		}
	}
	walk(fields, rec, "", 0, 0, 0)
	return res
}

// naivePages 独立按分页规则重放条目（每列每记录的条目切分由 rep==0 决定）。
func naivePages(entries []Entry, pageSize int, startRec int) []PageInfo {
	var pages []PageInfo
	cur := PageInfo{StartRecord: startRec}
	rec := startRec
	flush := func() {
		if cur.RecordCount > 0 {
			pages = append(pages, cur)
			cur = PageInfo{StartRecord: rec}
		}
	}
	n := 0
	for _, e := range entries {
		if e.Rep == 0 {
			if n > 0 && cur.EntryCount >= pageSize {
				flush()
			}
			if cur.RecordCount == 0 {
				cur.StartRecord = rec
			}
			cur.RecordCount++
			rec++
		}
		cur.EntryCount++
		n++
	}
	flush()
	return pages
}
