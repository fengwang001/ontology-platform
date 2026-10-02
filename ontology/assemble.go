package ontology

// Assemble reconstructs all records in record order from the leaf columns.
func (s *Shredder) Assemble() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := s.recordCount
	if n == 0 {
		return []map[string]any{}
	}

	totalEntries := 0
	streams := make([][]Entry, len(s.leaves))
	for i, c := range s.leafCols {
		streams[i] = c.entries
		totalEntries += len(c.entries)
	}

	records := make([]map[string]any, n)
	for i := range records {
		records[i] = map[string]any{}
	}

	for ci, lp := range s.leaves {
		b := &colBuilder{lp: lp, es: streams[ci]}
		for b.pos < len(b.es) {
			if b.es[b.pos].Rep != 0 {
				panicInconsistent(lp.path)
			}
			recIdx := b.recordIdx
			if recIdx >= n {
				panicInconsistent(lp.path)
			}
			built := b.buildRecord()
			mergeGroup(records[recIdx], built, []Field{*lp.fields[0]}, lp.path)
			b.recordIdx++
		}
	}

	s.entriesRead.Add(int64(totalEntries))
	return records
}

type colBuilder struct {
	lp        leafPath
	es        []Entry
	pos       int
	recordIdx int
}

// buildRecord builds one record's view of the leaf column, as a map
// rooted at the top-level field.
func (b *colBuilder) buildRecord() map[string]any {
	root := b.lp.fields[0]
	out := map[string]any{}
	if root.Rep == Repeated {
		if lst := b.buildRepeated(0, 0, 0); lst != nil {
			out[root.Name] = lst
		}
		return out
	}
	v := b.buildSegment(0, 0, 0)
	if v != absent {
		out[root.Name] = v
	}
	return out
}

// absent distinguishes a missing Optional from a present empty group.
var absent = new(struct{})

// buildSegment processes path segment seg inside an existing group.
// It returns the field value, or absent for a missing Optional.
func (b *colBuilder) buildSegment(seg, def, repIn int) any {
	f := b.lp.fields[seg]
	last := seg == len(b.lp.fields)-1

	if f.Rep == Optional {
		e := b.es[b.pos]
		if e.Def <= def {
			b.pos++
			return absent
		}
		def++
		if last {
			if e.Rep != repIn || e.Null {
				panicInconsistent(b.lp.path)
			}
			b.pos++
			return e.Value
		}
		return b.buildElementGroup(seg+1, def, repIn)
	}

	if f.Rep == Required {
		if last {
			return b.requireLeaf(repIn, def)
		}
		return b.buildElementGroup(seg+1, def, repIn)
	}

	// Repeated.
	lst := b.buildRepeated(seg, def, repIn)
	if lst == nil {
		return absent
	}
	return lst
}

// buildElementGroup builds the map for one element of a parent group.
// nextSeg is the first segment inside that element.
func (b *colBuilder) buildElementGroup(nextSeg, def, repIn int) map[string]any {
	g := map[string]any{}
	// All segments from nextSeg down to the leaf are chain-nested groups
	// (possibly Optional / Repeated); but only the immediate child is a
	// member key of g; deeper ones belong to nested groups.
	v := b.buildSegment(nextSeg, def, repIn)
	if v != absent {
		g[b.lp.fields[nextSeg].Name] = v
	}
	return g
}

func (b *colBuilder) requireLeaf(repIn, def int) int64 {
	e := b.es[b.pos]
	if e.Rep != repIn || e.Null || e.Def != def {
		panicInconsistent(b.lp.path)
	}
	b.pos++
	return e.Value
}

// buildRepeated processes a Repeated segment. Returns nil for an empty
// list, otherwise the constructed []any.
func (b *colBuilder) buildRepeated(seg, def, repIn int) []any {
	e := b.es[b.pos]
	if e.Def <= def {
		if e.Rep != repIn || !e.Null {
			panicInconsistent(b.lp.path)
		}
		b.pos++
		return nil
	}

	level := 0
	for k := 0; k <= seg; k++ {
		if b.lp.fields[k].Rep == Repeated {
			level++
		}
	}
	childDef := def + 1
	var out []any
	for {
		e := b.es[b.pos]
		if out != nil {
			if e.Rep < level {
				break
			}
			if e.Rep != level {
				panicInconsistent(b.lp.path)
			}
		} else if e.Rep != repIn || e.Rep > level {
			panicInconsistent(b.lp.path)
		}

		if seg == len(b.lp.fields)-1 {
			if e.Null || e.Def != childDef {
				panicInconsistent(b.lp.path)
			}
			out = append(out, e.Value)
			b.pos++
		} else {
			out = append(out, b.buildElementGroup(seg+1, childDef, e.Rep))
		}

		if b.pos >= len(b.es) {
			break
		}
	}
	return out
}

func panicInconsistent(path string) {
	panic(&FieldError{Kind: ErrInconsistent, Path: path})
}

func findField(fields []Field, name string) *Field {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

// mergeGroup merges a per-column reconstructed group src into dst.
func mergeGroup(dst, src map[string]any, fields []Field, path string) {
	for k, v := range src {
		f := findField(fields, k)
		if f == nil {
			panicInconsistent(path)
		}
		if existing, present := dst[k]; present {
			mergeValue(existing, v, f, path)
		} else {
			dst[k] = v
		}
	}
}

func mergeValue(a, b any, f *Field, path string) {
	if sv, ok := b.([]any); ok {
		ev, ok2 := a.([]any)
		if !ok2 || len(ev) != len(sv) {
			panicInconsistent(path)
		}
		if len(f.Children) == 0 {
			for j := range sv {
				if ev[j] != sv[j] {
					panicInconsistent(path)
				}
			}
			return
		}
		for j := range sv {
			dm, ok1 := ev[j].(map[string]any)
			sm, ok2 := sv[j].(map[string]any)
			if !ok1 || !ok2 {
				panicInconsistent(path)
			}
			mergeGroup(dm, sm, f.Children, path)
		}
		return
	}
	if sg, ok := b.(map[string]any); ok {
		eg, ok2 := a.(map[string]any)
		if !ok2 {
			panicInconsistent(path)
		}
		mergeGroup(eg, sg, f.Children, path)
		return
	}
	if a != b {
		panicInconsistent(path)
	}
}
