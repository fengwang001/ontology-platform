package defassign

// 朴素参考模型：枚举有界数量的具体执行路径，每条路径逐步模拟确定赋值集合
// 与无效赋值 may 状态 life，再按同一口径汇总。与 analyze.go 不共享分析代码，
// 仅共用类型、路径标签词表与校验器。

const naiveMaxIters = 4

type npath struct {
	assign map[string]bool
	life   life
	trace  string
}

type nresult struct {
	normal, breaks, rets []npath
	throws               life
}

type naiveModel struct {
	lines map[Pos]int
	vars  map[Pos]string
	maybe map[Pos]map[string]bool
	alive map[Pos]bool
}

// NaiveCheck 以朴素模型检查程序，返回与 Check 同形态的报告。
func NaiveCheck(p *Program) (*Report, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	m := &naiveModel{
		lines: map[Pos]int{}, vars: map[Pos]string{},
		maybe: map[Pos]map[string]bool{}, alive: map[Pos]bool{},
	}
	m.indexLines(p.Stmts)
	start := npath{assign: map[string]bool{}, life: newLife(), trace: traceRoot}
	res := m.runSeq(p.Stmts, []npath{start}, nil, nil)

	var diags []Diagnostic
	for pos, traces := range m.maybe {
		ws := make([]string, 0, len(traces))
		for t := range traces {
			ws = append(ws, t)
		}
		diags = append(diags, Diagnostic{Kind: DiagMaybeUnassigned, At: pos,
			Line: m.lines[pos], Var: m.vars[pos], Witness: ws})
	}
	all := newLife()
	all.union(res.throws)
	for _, g := range [][]npath{res.normal, res.breaks, res.rets} {
		for _, q := range g {
			all.union(q.life)
		}
	}
	emit := func(q Pos, v string) {
		if m.alive[q] {
			return
		}
		diags = append(diags, Diagnostic{Kind: DiagDeadAssign, At: q,
			Line: m.lines[q], Var: v, Witness: []string{traceRoot}})
	}
	for v, set := range all.pend {
		for q := range set {
			emit(q, v)
		}
	}
	for q := range all.gone {
		emit(q, m.vars[q])
	}

	rep := &Report{Program: p.Name, Diags: diags}
	rep.normalize()
	return rep, nil
}

func (m *naiveModel) indexLines(stmts []*Stmt) {
	for _, s := range stmts {
		m.lines[s.Pos] = s.Line
		if s.Kind == KAssign || s.Kind == KUse {
			m.vars[s.Pos] = s.Var
		}
		for _, g := range childGroups(s) {
			m.indexLines(g)
		}
	}
}

func cloneNPath(p npath) npath {
	return npath{assign: cloneSet(p.assign), life: p.life.clone(), trace: p.trace}
}

func cloneSet(s map[string]bool) map[string]bool {
	b := make(map[string]bool, len(s))
	for k := range s {
		b[k] = true
	}
	return b
}

func tagPath(p npath, tag string) npath {
	q := cloneNPath(p)
	q.trace += ">" + tag
	return q
}

func (m *naiveModel) runSeq(stmts []*Stmt, ps []npath, escapes *life, borders *int) nresult {
	var res nresult
	res.throws = newLife()
	cur := ps
	for _, s := range stmts {
		if len(cur) == 0 {
			break
		}
		next := make([]npath, 0, len(cur))
		for _, p := range cur {
			if escapes != nil {
				escapes.union(p.life)
			}
			if borders != nil {
				if canThrowAt(s.Kind) {
					*borders++
				}
				if s.Kind == KTry && cleanupThrowBorders(s.Cleanup) > 0 {
					*borders++
				}
			}
			r := m.step(s, p, escapes, borders)
			next = append(next, r.normal...)
			res.breaks = append(res.breaks, r.breaks...)
			res.rets = append(res.rets, r.rets...)
			if escapes != nil {
				res.throws.union(r.throws)
			}
		}
		cur = next
	}
	res.normal = cur
	return res
}

func (m *naiveModel) step(s *Stmt, p npath, outer *life, borders *int) nresult {
	switch s.Kind {
	case KDeclare:
		return nresult{normal: []npath{cloneNPath(p)}}
	case KAssign:
		q := cloneNPath(p)
		q.assign[s.Var] = true
		q.life.put(s.Var, s.Pos)
		return nresult{normal: []npath{q}}
	case KUse:
		q := cloneNPath(p)
		if !q.assign[s.Var] {
			if m.maybe[s.Pos] == nil {
				m.maybe[s.Pos] = map[string]bool{}
			}
			m.maybe[s.Pos][q.trace] = true
		} else {
			for _, pos := range q.life.read(s.Var) {
				m.alive[pos] = true
			}
		}
		_ = q.life.pend.consume(s.Var)
		return nresult{normal: []npath{q}}
	case KIf:
		out := nresult{throws: newLife()}
		add := func(r nresult) {
			out.normal = append(out.normal, r.normal...)
			out.breaks = append(out.breaks, r.breaks...)
			out.rets = append(out.rets, r.rets...)
			out.throws.union(r.throws)
		}
		if s.CondConst >= 0 {
			add(m.runSeq(s.Body, []npath{tagPath(p, tagIfThen)}, outer, borders))
		}
		if s.CondConst <= 0 {
			add(m.runSeq(s.Else, []npath{tagPath(p, tagIfElse)}, outer, borders))
		}
		return out
	case KLoop:
		return m.stepLoop(s, p, outer, borders)
	case KBreak:
		return nresult{breaks: []npath{tagPath(p, tagViaBreak)}}
	case KReturn:
		return nresult{rets: []npath{tagPath(p, tagViaReturn)}}
	case KTry:
		return m.stepTry(s, p, outer)
	}
	return nresult{}
}

func (m *naiveModel) stepLoop(s *Stmt, p npath, outer *life, borders *int) nresult {
	out := nresult{throws: newLife()}
	min := 0
	if s.AtLeast1 {
		min = 1
	}
	for n := min; n <= naiveMaxIters; n++ {
		if n == 0 {
			q := tagPath(p, tagLoopZero)
			out.normal = append(out.normal, q)
			continue
		}
		cur := []npath{{assign: cloneSet(p.assign), life: p.life.clone(),
			trace: p.trace + ">" + tagLoopIter}}
		var body nresult
		for it := 0; it < n; it++ {
			body = m.runSeq(s.Body, cur, outer, borders)
			if it+1 < n {
				cur = cur[:0]
				for _, q := range body.normal {
					cur = append(cur, npath{assign: cloneSet(p.assign), life: q.life.clone(),
						trace: p.trace + ">" + tagLoopIter})
				}
			}
		}
		out.normal = append(out.normal, body.normal...)
		for _, q := range body.breaks {
			out.normal = append(out.normal, npath{assign: q.assign, life: q.life,
				trace: trimSuffixTag(q.trace, tagViaBreak)})
			out.breaks = append(out.breaks, q)
		}
		out.rets = append(out.rets, body.rets...)
		out.throws.union(body.throws)
	}
	return out
}

func (m *naiveModel) stepTry(s *Stmt, p npath, outer *life) nresult {
	bodyThrows := newLife()
	borders := 0
	body := m.runSeq(s.Body, []npath{cloneNPath(p)}, &bodyThrows, &borders)
	bodyThrows.union(p.life)

	handlerEntry := p.life.clone()
	handlerEntry.union(bodyThrows)

	var hNormal, hBrk, hRet []npath
	hBorders := 0
	for i := range s.Handlers {
		entry := npath{assign: cloneSet(p.assign), life: handlerEntry.clone(),
			trace: p.trace + ">" + handlerTag(i)}
		hThrows := newLife()
		h := m.runSeq(s.Handlers[i], []npath{entry}, &hThrows, &hBorders)
		hNormal = append(hNormal, h.normal...)
		hBrk = append(hBrk, h.breaks...)
		hRet = append(hRet, h.rets...)
		handlerEntry.union(hThrows)
	}
	borders += hBorders

	var normalIn, brkIn, retIn []npath
	for _, q := range body.normal {
		normalIn = append(normalIn, tagPath(q, tagNormalBody))
	}
	for _, q := range hNormal {
		normalIn = append(normalIn, tagPath(q, tagHandlerEnd))
	}
	if borders > 0 {
		normalIn = append(normalIn, npath{assign: cloneSet(p.assign),
			life: handlerEntry.clone(), trace: p.trace + ">" + tagUncaught})
	}
	for _, q := range body.breaks {
		q.trace = trimSuffixTag(q.trace, tagViaBreak)
		brkIn = append(brkIn, q)
	}
	for _, q := range hBrk {
		q.trace = trimSuffixTag(q.trace, tagViaBreak)
		brkIn = append(brkIn, q)
	}
	for _, q := range body.rets {
		q.trace = trimSuffixTag(q.trace, tagViaReturn)
		retIn = append(retIn, q)
	}
	for _, q := range hRet {
		q.trace = trimSuffixTag(q.trace, tagViaReturn)
		retIn = append(retIn, q)
	}

	out := nresult{throws: newLife()}
	runClean := func(in []npath, fall string) {
		if len(in) == 0 {
			return
		}
		var tagged []npath
		for _, q := range in {
			tagged = append(tagged, tagPath(q, tagCleanup))
		}
		r := m.runSeq(s.Cleanup, tagged, outer, nil)
		for _, q := range r.normal {
			switch fall {
			case tagBreak:
				q.trace += ">" + tagBreak
				out.breaks = append(out.breaks, q)
			case tagReturn:
				q.trace += ">" + tagReturn
				out.rets = append(out.rets, q)
			default:
				out.normal = append(out.normal, q)
			}
		}
		out.breaks = append(out.breaks, r.breaks...)
		out.rets = append(out.rets, r.rets...)
		out.throws.union(r.throws)
	}
	runClean(normalIn, "normal")
	runClean(brkIn, tagBreak)
	runClean(retIn, tagReturn)
	return out
}
