package ontology

// emitColumn produces the entries of one record for a single leaf path.
// repIn is the rep inherited from outer repeated elements.
func emitColumn(lp leafPath, rec map[string]any) []Entry {
	out := make([]Entry, 0, 1)
	var emit func(segIdx int, node any, def, repIn int)

	emit = func(segIdx int, node any, def, repIn int) {
		f := lp.fields[segIdx]
		last := segIdx == len(lp.fields)-1

		if f.Rep == Repeated {
			var arr []any
			arr, _ = childValue(node, f.Name).([]any)
			if len(arr) == 0 {
				out = append(out, Entry{Rep: repIn, Def: def, Null: true})
				return
			}
			repeatLevel := 0
			for k := 0; k <= segIdx; k++ {
				if lp.fields[k].Rep == Repeated {
					repeatLevel++
				}
			}
			for j, elem := range arr {
				childRep := repIn
				if j >= 1 {
					childRep = repeatLevel
				}
				if last {
					out = append(out, Entry{Rep: childRep, Def: def + 1, Value: elem.(int64)})
				} else {
					emit(segIdx+1, elem, def+1, childRep)
				}
			}
			return
		}

		var (
			v       any
			present bool
		)
		v, present = childValue2(node, f.Name)
		if f.Rep == Optional {
			if !present || v == nil {
				out = append(out, Entry{Rep: repIn, Def: def, Null: true})
				return
			}
			def++
		}
		if last {
			out = append(out, Entry{Rep: repIn, Def: def, Value: v.(int64)})
			return
		}
		emit(segIdx+1, v, def, repIn)
	}

	emit(0, rec, 0, 0)
	return out
}

func childValue(node any, name string) any {
	if m, ok := node.(map[string]any); ok {
		return m[name]
	}
	return nil
}

func childValue2(node any, name string) (any, bool) {
	if m, ok := node.(map[string]any); ok {
		v, p := m[name]
		return v, p
	}
	return nil, false
}
