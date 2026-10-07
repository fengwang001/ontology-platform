package bitemporal

import "sort"

// 本文件提供一个刻意“朴素”的独立参考实现：
// 对每次回放全量扫描该链接类型的所有事实（O(F)），不共享任何预计算结构。
// 生产路径不调用它；随机差分测试用它与索引实现逐条对照。

// NaiveReplay 以朴素方式回放 (rt, vt) 的链接集合与结构缺陷。
func (s *Snapshot) NaiveReplay(linkType ID, rt, vt int64) ReplayResult {
	tk, ok := s.types[linkType]
	if !ok {
		return ReplayResult{}
	}
	type state struct {
		pos bool
		neg bool
	}
	pairs := map[pairKey]*state{}
	get := func(key pairKey) *state {
		st := pairs[key]
		if st == nil {
			st = &state{}
			pairs[key] = st
		}
		return st
	}

	fs := append([]linkFact(nil), s.facts[linkType]...)
	sort.SliceStable(fs, func(i, j int) bool { return factLess(fs[i], fs[j]) })
	for _, f := range fs {
		if f.recordTime > rt || f.validTime > vt {
			continue
		}
		key := pairKey{a: f.src, b: f.dst}
		get(key)
		h := halfName(f.symmetric, f.token)
		if f.origin == "c" {
			if f.symmetric {
				h = halfAB
			} else {
				h = halfF
			}
		}
		alive := f.kind == kindCreate
		apply := func(side string) {
			st := get(key)
			if tk.lt.Symmetric {
				if side == halfAB {
					st.pos = alive
				} else {
					st.neg = alive
				}
			} else if side == halfF {
				st.pos = alive
			} else {
				st.neg = alive
			}
		}
		apply(h)
		if f.origin == "c" {
			apply(otherHalf(h))
		}
	}

	var keys []pairKey
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})

	res := ReplayResult{}
	for _, key := range keys {
		st := pairs[key]
		switch {
		case st.pos && st.neg:
			if tk.lt.Symmetric {
				res.Edges = append(res.Edges,
					Edge{LinkType: linkType, From: key.a, To: key.b},
					Edge{LinkType: linkType, From: key.b, To: key.a})
			} else {
				res.Edges = append(res.Edges, Edge{LinkType: linkType, From: key.a, To: key.b})
			}
		case st.pos || st.neg:
			missing := halfBA
			if !tk.lt.Symmetric {
				missing = halfR
			}
			if st.neg {
				if tk.lt.Symmetric {
					missing = halfAB
				} else {
					missing = halfF
				}
			}
			res.Defects = append(res.Defects, MirrorDefect{
				LinkType: linkType, A: key.a, B: key.b,
				MissingSide: missing, ValidTime: vt, RecordTime: rt,
			})
		}
	}
	sortEdges(res.Edges)
	sort.Slice(res.Defects, func(i, j int) bool {
		if res.Defects[i].A != res.Defects[j].A {
			return res.Defects[i].A < res.Defects[j].A
		}
		return res.Defects[i].B < res.Defects[j].B
	})
	return res
}

// NaiveAudit 以朴素方式计算对角线审计在单个记录时间点的违反集合。
func (s *Snapshot) NaiveAudit(linkType ID, rt int64) (LinkTypeRule, []Violation, []MirrorDefect) {
	tk, ok := s.types[linkType]
	if !ok {
		return LinkTypeRule{}, nil, nil
	}
	rule, hasRule := s.RuleAt(linkType, rt)
	if !hasRule {
		return LinkTypeRule{}, nil, nil
	}
	res := s.NaiveReplay(linkType, rt, rt)
	fwdDeg := map[ID]int{}
	revDeg := map[ID]int{}
	for _, e := range res.Edges {
		// NaiveReplay 对对称边产出两条有向边，非对称产出一条。
		if e.From != "" {
			if tk.lt.Symmetric {
				fwdDeg[e.From]++
				revDeg[e.To]++
			} else {
				fwdDeg[e.From]++
				revDeg[e.To]++
			}
		}
	}
	viol := func(deg map[ID]int, card Card, dir string) []Violation {
		var out []Violation
		var ids []ID
		for id := range deg {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			d := deg[id]
			if d < card.Min || (card.Max != 0 && d > card.Max) {
				out = append(out, Violation{Direction: dir, Object: id, Outgoing: d, Card: card})
			}
		}
		return out
	}
	var vs []Violation
	vs = append(vs, viol(fwdDeg, rule.Card.Forward, "forward")...)
	vs = append(vs, viol(revDeg, rule.Card.Reverse, "reverse")...)
	return rule, vs, res.Defects
}
