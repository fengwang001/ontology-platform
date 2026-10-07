package bitemporal

import "sort"

func buildSegments(snap *Snapshot, tk *typeKey, idx *linkIndex, req AuditRequest) []Segment {
	rule, _ := snap.RuleAt(req.LinkType, req.RecordStart)
	ri := 0
	for i, r := range tk.rules {
		if r.FromRecord == rule.FromRecord {
			ri = i
			break
		}
	}
	var segs []Segment
	for ; ri < len(tk.rules); ri++ {
		r := tk.rules[ri]
		start := req.RecordStart
		if r.FromRecord > start {
			start = r.FromRecord
		}
		end := req.RecordEnd
		if ri+1 < len(rulesOf(tk)) && rulesOf(tk)[ri+1].FromRecord < end {
			end = rulesOf(tk)[ri+1].FromRecord
		}
		if start >= end {
			break
		}
		segs = append(segs, scanEra(idx, ri, start, end, r, req.ValidTime)...)
	}
	return mergeAdjacent(segs)
}

func rulesOf(tk *typeKey) []LinkTypeRule { return tk.rules }

// scanEra 用每个对象的净度事件流驱动分段：
// 违反集合或违反对象度值在任何 tick 改变都切出新分段。
func scanEra(idx *linkIndex, eraIdx int, start, end int64, rule LinkTypeRule, fixedVT int64) []Segment {
	era := idx.eras[eraIdx]

	type state struct {
		dir    string
		obj    ID
		degree int
	}
	cardOf := func(dir string) Card {
		if dir == "reverse" {
			return rule.Card.Reverse
		}
		return rule.Card.Forward
	}
	violated := func(dir string, d int) bool {
		c := cardOf(dir)
		return d < c.Min || (c.Max != 0 && d > c.Max)
	}

	// 收集窗口内所有度变化 tick（只针对可能违反的对象，但为报告度值，
	// 任何在窗口内一度违反的对象其全部变化 tick 都纳入）。
	type tickRef struct {
		at  int64
		dir string
		obj ID
	}
	var refs []tickRef

	consider := func(dir string, objs []ID, evs map[ID][]degTick) {
		for _, obj := range objs {
			d := 0
			everBad := false
			for _, e := range evs[obj] {
				if e.at <= start {
					d += e.delta
				}
			}
			if violated(dir, d) {
				everBad = true
			}
			for _, e := range evs[obj] {
				if e.at > start && e.at < end {
					d += e.delta
					if violated(dir, d) {
						everBad = true
					}
				}
			}
			if !everBad {
				continue
			}
			for _, e := range evs[obj] {
				if e.at > start && e.at < end {
					refs = append(refs, tickRef{at: e.at, dir: dir, obj: obj})
				}
			}
		}
	}
	consider("forward", era.fwdObjs, era.evFwd)
	consider("reverse", era.revObjs, era.evRev)
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].at < refs[j].at })

	degreeAt2 := func(dir string, obj ID, t int64, evs map[ID][]degTick) int {
		d := 0
		for _, e := range evs[obj] {
			if e.at <= t {
				d += e.delta
			}
		}
		return d
	}

	snapshot := func(t int64) []Violation {
		var out []Violation
		emit := func(dir string, objs []ID, evs map[ID][]degTick) {
			card := cardOf(dir)
			var ids []ID
			byObj := map[ID]int{}
			for _, obj := range objs {
				d := degreeAt2(dir, obj, t, evs)
				if violated(dir, d) {
					byObj[obj] = d
					ids = append(ids, obj)
				}
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids {
				out = append(out, Violation{
					Direction: dir, Object: id, Outgoing: byObj[id], Card: card,
				})
			}
		}
		emit("forward", era.fwdObjs, era.evFwd)
		emit("reverse", era.revObjs, era.evRev)
		return out
	}

	var segs []Segment
	cur := start
	prev := snapshot(cur)
	push := func(next int64) {
		if next <= cur {
			return
		}
		vt := fixedVT
		if fixedVT == 0 {
			vt = cur
		}
		segs = append(segs, Segment{
			RecordStart: cur, RecordEnd: next, ValidTime: vt,
			Rule: rule, Violations: prev,
		})
		cur = next
	}

	i := 0
	for i < len(refs) {
		t := refs[i].at
		push(t)
		for i < len(refs) && refs[i].at == t {
			i++
		}
		prev = snapshot(t)
	}
	push(end)
	return segs
}

func mergeAdjacent(segs []Segment) []Segment {
	if len(segs) <= 1 {
		return segs
	}
	out := segs[:1]
	for i := 1; i < len(segs); i++ {
		last := &out[len(out)-1]
		if last.RecordEnd == segs[i].RecordStart &&
			last.Rule.FromRecord == segs[i].Rule.FromRecord &&
			last.ValidTime == segs[i].ValidTime &&
			violEqual(last.Violations, segs[i].Violations) {
			last.RecordEnd = segs[i].RecordEnd
			continue
		}
		out = append(out, segs[i])
	}
	return out
}

func violEqual(a, b []Violation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
