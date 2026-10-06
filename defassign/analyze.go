package defassign

// 路径状态传播器（确定赋值 + 无效赋值）。
//
// 每条「路径车道」lane 携带两个状态：
//   - assign：该组路径的确定已赋值集合（同组必相同）；
//   - life：该组具体路径的无效赋值 may 状态（pend=各变量最近一次未读赋值，
//     gone=已被同变量后续赋值覆盖的旧赋值）。
// 车道按「相同 assign 集合 + 相同 life 状态」合并（traces 取并），从而：
// 读取点只遍历到达它的车道；汇合开销只涉及实际到达的车道与变量；
// 一条路径上的覆盖不会让另一条路径上仍待读的赋值误判无效。

const traceRoot = "entry"

type lane struct {
	assign assigned
	life   life
	traces []string
}

type exits struct {
	breaks []lane
	rets   []lane
	throws []lane // 逃逸抛出车道（携带其抛出点前缀状态）
}

type analyzer struct {
	stmtLine map[Pos]int
	stmtVar  map[Pos]string
	diags    []Diagnostic
	alive    map[Pos]bool
	// readLaneHits：每个读取点被检查的车道数（=到达该点的汇合车道数）。
	// 用于可验证地证明单读取点开销与程序总长无关。
	readLaneHits map[Pos]int
	mergeTouches int // mergeLanes 处理过的车道条目总数
}

// CheckResult 额外暴露内部计数，供复杂度性质的可验证测试使用。
type CheckResult struct {
	Report        *Report
	ReadLaneHits  map[Pos]int
	MergeOperands int
}

// Check 检查程序；任何输入错误整体拒绝。无跨程序状态，并发安全。
func Check(p *Program) (*Report, error) {
	cr, err := CheckDetailed(p)
	if err != nil {
		return nil, err
	}
	return cr.Report, nil
}

// CheckDetailed 同 Check，并返回可用于复杂度验证的内部计数。
func CheckDetailed(p *Program) (*CheckResult, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	a := &analyzer{
		stmtLine:     map[Pos]int{},
		stmtVar:      map[Pos]string{},
		alive:        map[Pos]bool{},
		readLaneHits: map[Pos]int{},
	}
	a.indexLines(p.Stmts)
	root := lane{assign: emptyAssigned(), life: newLife(), traces: []string{traceRoot}}
	normal, ex := a.analyzeSeq(p.Stmts, []lane{root}, nil)
	a.emitDead(normal, ex)
	rep := &Report{Program: p.Name, Diags: a.diags}
	rep.normalize()
	hits := make(map[Pos]int, len(a.readLaneHits))
	for k, v := range a.readLaneHits {
		hits[k] = v
	}
	return &CheckResult{Report: rep, ReadLaneHits: hits, MergeOperands: a.mergeTouches}, nil
}

func CheckText(name, src string) (*Report, error) {
	p, err := Parse(name, src)
	if err != nil {
		return nil, err
	}
	return Check(p)
}

func (a *analyzer) indexLines(stmts []*Stmt) {
	for _, s := range stmts {
		a.stmtLine[s.Pos] = s.Line
		if s.Kind == KAssign || s.Kind == KUse {
			a.stmtVar[s.Pos] = s.Var
		}
		for _, g := range childGroups(s) {
			a.indexLines(g)
		}
	}
}

// mergeLanes 合并状态等价的车道（assign 与 life 的指纹都相同），
// traces 取并。只比较实际到达的车道。
func mergeLanesFree(groups ...[]lane) []lane {
	return mergeLanesModeFree(groups, true)
}

func mergeLanesModeFree(groups [][]lane, mergeTraces bool) []lane {
	var merged []lane
	index := map[uint64][]int{}
	for _, g := range groups {
		for _, l := range g {
			key := laneFingerprint(l.assign, l.life)
			slot := -1
			for _, cand := range index[key] {
				m := merged[cand]
				if sameAssigned(m.assign, l.assign) && equalLife(m.life, l.life) {
					slot = cand
					break
				}
			}
			if slot < 0 {
				index[key] = append(index[key], len(merged))
				merged = append(merged, lane{assign: l.assign, life: l.life,
					traces: append([]string(nil), l.traces...)})
			} else if mergeTraces {
				merged[slot].traces = append(merged[slot].traces, l.traces...)
			}
		}
	}
	return merged
}

func (a *analyzer) mergeLanes(groups ...[]lane) []lane {
	return a.mergeLanesMode(groups, true)
}

func (a *analyzer) mergeLanesMode(groups [][]lane, mergeTraces bool) []lane {
	var merged []lane
	index := map[uint64][]int{}
	for _, g0 := range groups {
		g := g0
		for _, l := range g {
			a.mergeTouches++
			key := laneFingerprint(l.assign, l.life)
			slot := -1
			for _, cand := range index[key] {
				m := merged[cand]
				if sameAssigned(m.assign, l.assign) && equalLife(m.life, l.life) {
					slot = cand
					break
				}
			}
			if slot < 0 {
				index[key] = append(index[key], len(merged))
				merged = append(merged, lane{
					assign: l.assign, life: l.life,
					traces: append([]string(nil), l.traces...),
				})
			} else if mergeTraces {
				merged[slot].traces = append(merged[slot].traces, l.traces...)
			}
		}
	}
	return merged
}

func laneFingerprint(s assigned, l life) uint64 {
	h := setFingerprint(s)
	h ^= lifeFingerprint(l)
	return h
}

func setFingerprint(s assigned) uint64 {
	var h uint64 = 1469598103934665603
	for k := range s {
		for i := 0; i < len(k); i++ {
			h ^= uint64(k[i])
			h *= 1099511628211
		}
		h ^= 0xff
		h *= 1099511628211
	}
	h ^= uint64(len(s))
	return h
}

func lifeFingerprint(l life) uint64 {
	var h uint64 = 1469598103934665603
	hashPos := func(p Pos) {
		h ^= uint64(p)
		h *= 1099511628211
	}
	for v, set := range l.pend {
		for i := 0; i < len(v); i++ {
			h ^= uint64(v[i])
			h *= 1099511628211
		}
		for p := range set {
			hashPos(p)
		}
	}
	for p := range l.gone {
		hashPos(p)
	}
	return h
}

func sameAssigned(a, b assigned) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func cloneLane(l lane) lane {
	return lane{assign: l.assign.clone(), life: l.life.clone(),
		traces: append([]string(nil), l.traces...)}
}

func cloneLanes(ls []lane) []lane {
	out := make([]lane, len(ls))
	for i, l := range ls {
		out[i] = cloneLane(l)
	}
	return out
}

func appendTag(traces []string, tag string) []string {
	out := make([]string, len(traces))
	for i, t := range traces {
		out[i] = t + ">" + tag
	}
	return out
}

func forkLanes(ls []lane, tag string) []lane {
	out := make([]lane, len(ls))
	for i, l := range ls {
		traces := appendTag(l.traces, tag)
		if tag == tagLoopIter {
			traces = canonTraces(traces)
		}
		out[i] = lane{assign: l.assign.clone(), life: l.life.clone(), traces: traces}
	}
	return out
}

func retag(ls []lane, tag string) []lane {
	out := make([]lane, len(ls))
	for i, l := range ls {
		out[i] = lane{assign: l.assign.clone(), life: l.life.clone(),
			traces: appendTag(l.traces, tag)}
	}
	return out
}

func trimSuffixTag(t, suffix string) string {
	want := ">" + suffix
	if len(t) >= len(want) && t[len(t)-len(want):] == want {
		return t[:len(t)-len(want)]
	}
	return t
}

func stripTag(ls []lane, suffix string) []lane {
	out := make([]lane, len(ls))
	for i, l := range ls {
		tr := make([]string, len(l.traces))
		for j, t := range l.traces {
			tr[j] = trimSuffixTag(t, suffix)
		}
		out[i] = lane{assign: l.assign.clone(), life: l.life.clone(), traces: tr}
	}
	return out
}

func anyLaneAssigned(ls []lane, v string) bool {
	for _, l := range ls {
		if l.assign[v] {
			return true
		}
	}
	return false
}

func handlerTag(i int) string { return tagHandler + "[" + itoa(i) + "]" }

// canonTrace 规范化路径标签：连续重复的 loop.iter 折叠为一个
// （各轮入口 assigned 相同，轮次不是客观可区分的路径）。
func canonTrace(t string) string {
	parts := splitTrace(t)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == tagLoopIter && len(out) > 0 && out[len(out)-1] == tagLoopIter {
			continue
		}
		out = append(out, p)
	}
	res := out[0]
	for _, p := range out[1:] {
		res += ">" + p
	}
	return res
}

func splitTrace(t string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(t); i++ {
		if t[i] == '>' {
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return append(parts, t[start:])
}

func canonTraces(ts []string) []string {
	seen := map[string]bool{}
	for _, t := range ts {
		seen[canonTrace(t)] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	return out
}

// analyzeSeq 返回正常结束车道与旁路出口集合。throwsOut 非 nil 时，
// 在每条可抛出语句边界（车道仍正常时）追加一条抛出车道。
func (a *analyzer) analyzeSeq(stmts []*Stmt, lanes []lane, throwsOut *[]lane) ([]lane, exits) {
	var ex exits
	cur := lanes
	reachable := true

	absorb := func(e exits) {
		ex.breaks = a.mergeLanes(ex.breaks, e.breaks)
		ex.rets = a.mergeLanes(ex.rets, e.rets)
		ex.throws = a.mergeLanes(ex.throws, e.throws)
	}

	for _, s := range stmts {
		if !reachable {
			break
		}
		// 可抛出边界（assign/use；含会逃逸的嵌套 try 清理）：执行该语句前，
		// 向抛出收集器追加一条与当前正常车道同状态、同 trace 的抛出车道。
		if throwsOut != nil {
			switch s.Kind {
			case KAssign, KUse:
				*throwsOut = a.mergeLanes(*throwsOut, cloneLanes(cur))
			case KTry:
				if cleanupThrowBorders(s.Cleanup) > 0 {
					*throwsOut = a.mergeLanes(*throwsOut, cloneLanes(cur))
				}
			}
		}
		switch s.Kind {
		case KDeclare:
		case KAssign:
			for i := range cur {
				cur[i].assign[s.Var] = true
				cur[i].life.put(s.Var, s.Pos)
			}
			cur = a.mergeLanes(cur)
		case KUse:
			a.readLaneHits[s.Pos] += len(cur)
			for i := range cur {
				if !cur[i].assign[s.Var] {
					a.diags = append(a.diags, Diagnostic{
						Kind: DiagMaybeUnassigned, At: s.Pos, Line: a.stmtLine[s.Pos],
						Var: s.Var, Witness: append([]string(nil), cur[i].traces...),
					})
				} else {
					for _, q := range cur[i].life.read(s.Var) {
						a.alive[q] = true
					}
					_ = cur[i].life.pend.consume(s.Var)
				}
			}
			cur = a.mergeLanes(cur)
		case KIf:
			var normal [][]lane
			var subExits []exits
			if s.CondConst >= 0 {
				n, e := a.analyzeSeq(s.Body, forkLanes(cur, tagIfThen), throwsOut)
				normal = append(normal, n)
				subExits = append(subExits, e)
			}
			if s.CondConst <= 0 {
				n, e := a.analyzeSeq(s.Else, forkLanes(cur, tagIfElse), throwsOut)
				normal = append(normal, n)
				subExits = append(subExits, e)
			}
			cur = a.mergeLanes(normal...)
			reachable = len(cur) > 0
		case KLoop:
			n, e := a.analyzeLoop(s, cur, throwsOut)
			cur = n
			absorb(e)
			reachable = len(cur) > 0
		case KBreak:
			ex.breaks = a.mergeLanes(ex.breaks, retag(cur, tagViaBreak))
			reachable = false
		case KReturn:
			ex.rets = a.mergeLanes(ex.rets, retag(cur, tagViaReturn))
			reachable = false
		case KTry:
			n, e := a.analyzeTry(s, cur)
			cur = n
			absorb(e)
			reachable = len(cur) > 0
		}
	}
	if !reachable {
		cur = nil
	}
	return a.mergeLanes(cur), ex
}

// emitDead 在所有终止车道（正常/跳出/返回/逃逸抛出）上汇总无效赋值：
// 某赋值点在每条终止车道上都终结于 pend 或 gone、且从未被读取证伪，则无效。
func (a *analyzer) emitDead(normal []lane, ex exits) {
	type term struct {
		vars map[Pos]string
		life life
	}
	var terms []term
	add := func(ls []lane) {
		for _, l := range ls {
			vars := map[Pos]string{}
			for v, set := range l.life.pend {
				for q := range set {
					vars[q] = v
				}
			}
			for q := range l.life.gone {
				vars[q] = a.stmtVar[q]
			}
			terms = append(terms, term{vars: vars, life: l.life})
		}
	}
	add(normal)
	add(ex.breaks)
	add(ex.rets)
	add(ex.throws)

	all := map[Pos]bool{}
	for _, t := range terms {
		for q := range t.vars {
			all[q] = true
		}
	}
	for q := range all {
		if a.alive[q] {
			continue
		}
		var v string
		onSome := false
		for _, t := range terms {
			if tv, ok := t.vars[q]; ok {
				onSome = true
				v = tv
			}
		}
		// 只在「赋值实际发生过的终止车道」上判定：未经过该赋值的车道
		// （如零次循环、中途抛出在赋值前）与无效性无关。
		if onSome {
			a.diags = append(a.diags, Diagnostic{
				Kind: DiagDeadAssign, At: q, Line: a.stmtLine[q],
				Var: v, Witness: []string{traceRoot},
			})
		}
	}
}

// analyzeLoop 口径：零次执行车道（loop1 除外）；每轮入口 assigned 恒为循环前
// 状态；life 入口在回边上取并至固定点（下一轮开头可能读取可证伪体内赋值）；
// break 车道只汇入循环之后。
func (a *analyzer) analyzeLoop(s *Stmt, lanes []lane, outerThrows *[]lane) ([]lane, exits) {
	entryLanes := cloneLanes(lanes)
	var bodyNormal []lane
	var bodyExits exits
	iter := 0
	for {
		iter++
		in := forkLanes(entryLanes, tagLoopIter)
		n, e := a.analyzeSeq(s.Body, in, outerThrows)
		bodyExits = e
		// 回边车道：assigned 重置为循环前状态，life 取「入口∪本轮结束」。
		backed := make([]lane, len(n))
		for i, l := range n {
			backed[i] = lane{assign: assignByPrefix(lanes, l.traces, tagLoopIter), life: l.life,
				traces: stripOneTag(l.traces, tagLoopIter)}
		}
		// 回边只承载「本轮正常结束」的车道；break/return 已离开循环。
		canonTracesInLanes(backed)
		merged := a.mergeLanes(entryLanes, backed)
		if lanesStateEqual(merged, entryLanes) || iter >= loopFixedPointCap {
			bodyNormal, bodyExits = n, e
			break
		}
		entryLanes = merged
	}

	var groups [][]lane
	if !s.AtLeast1 {
		groups = append(groups, forkLanes(lanes, tagLoopZero))
	}
	groups = append(groups, bodyNormal)
	// break 实际到达循环之后：其 assigned 参与循环后汇合。
	if len(bodyExits.breaks) > 0 {
		groups = append(groups, stripTag(bodyExits.breaks, tagViaBreak))
	}
	out := a.mergeLanes(groups...)
	// break 已被本循环消费；返回/抛出继续外送。
	ex := exits{rets: bodyExits.rets, throws: bodyExits.throws}
	return out, ex
}

// loopFixedPointCap 是回边固定点的防御上限：状态格有限（每个赋值点只在
// 某个 lane 的 pend/gone 中），正常极少迭代即收敛。
const loopFixedPointCap = 64

// lanesStateEqual 只按 (assign,life) 状态比较，忽略 traces（诊断依据）。
func lanesStateEqual(x, y []lane) bool {
	ma := mergeLanesFree(x)
	mb := mergeLanesFree(y)
	if len(ma) != len(mb) {
		return false
	}
	keyOf := func(ls []lane) map[uint64]int {
		m := map[uint64]int{}
		for _, l := range ls {
			m[laneFingerprint(l.assign, l.life)]++
		}
		return m
	}
	ca, cb := keyOf(ma), keyOf(mb)
	if len(ca) != len(cb) {
		return false
	}
	for k, v := range ca {
		if cb[k] != v {
			return false
		}
	}
	return true
}

func canonTracesInLanes(ls []lane) {
	for i := range ls {
		ls[i].traces = canonTraces(ls[i].traces)
	}
}

// assignByPrefix 按回边车道的 trace 前缀找回其循环前车道的 assigned 集合。
func assignByPrefix(pre []lane, traces []string, lastTag string) assigned {
	if len(pre) == 1 {
		return pre[0].assign.clone()
	}
	prefix := trimSuffixTag(traces[0], lastTag)
	for _, l := range pre {
		for _, t := range l.traces {
			if t == prefix {
				return l.assign.clone()
			}
		}
	}
	// 车道在循环体内已汇合：所有对应循环前车道的 assigned 交集即公共基准。
	return intersectAll(extractAssigned(pre)).clone()
}

// preAssignByTrace 按抛出车道的 trace（其 try 前前缀是去掉最内层标签后的
// 串）找回进入 try 前车道的 assigned；找不到（车道在体内汇合）时取交集。
func preAssignByTrace(in []lane, traces []string) assigned {
	if len(in) == 1 {
		return in[0].assign.clone()
	}
	// 抛出车道 trace 的前缀必以某条入口车道 trace 开头。
	for _, l := range in {
		for _, et := range l.traces {
			for _, t := range traces {
				if hasPrefix(t, et+">") {
					return l.assign.clone()
				}
			}
		}
	}
	return intersectAll(extractAssigned(in)).clone()
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func extractAssigned(ls []lane) []assigned {
	out := make([]assigned, len(ls))
	for i, l := range ls {
		out[i] = l.assign
	}
	return out
}

func stripOneTag(ts []string, tag string) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = trimSuffixTag(t, tag)
	}
	return out
}

// analyzeTry 实现异常保护结构：
//   - 处理分支入口 assigned 恒为进入前状态；其 life 为进入前 life 与被保护体
//     各抛出前缀 life 的按车道并（每条抛出车道各自携带）；
//   - 清理入口车道 = 正常体结束 ∪ 各分支结束 ∪ 未处理抛出车道（在存在可抛出
//     边界时）；跳出/返回经清理保持终止方式；
//   - 只有清理正常结束的车道到达结构之后。
func (a *analyzer) analyzeTry(s *Stmt, in []lane) ([]lane, exits) {
	// 被保护体：本地收集抛出前缀车道（不逃逸到外层）。
	var throws []lane
	body, bodyEx := a.analyzeSeq(s.Body, cloneLanes(in), &throws)

	// 每条抛出前缀车道派生各处理分支，入口 assigned 重置为该前缀对应的
	// try 前状态（被保护体任意点抛出 => 只认进入前赋值）。
	// 处理分支只有在「确有抛出边界」时才可能进入；被保护体无任何语句时
	// （或边界全部不可达）throws 为空，分支不产生诊断也不参与汇合。
	var hNormal, hBreaks, hRets []lane
	for i := range s.Handlers {
		var hIn []lane
		if len(throws) > 0 {
			for _, th := range throws {
				base := preAssignByTrace(in, th.traces)
				hIn = append(hIn, lane{
					assign: base,
					life:   th.life.clone(),
					traces: appendTag(th.traces, handlerTag(i)),
				})
			}
		} else {
			// 语法上存在处理分支但无抛出点：分支不可达，不分析。
		}
		var hThrows []lane
		h, hEx := a.analyzeSeq(s.Handlers[i], hIn, &hThrows)
		hNormal = a.mergeLanes(hNormal, h)
		hBreaks = a.mergeLanes(hBreaks, stripTag(hEx.breaks, tagViaBreak))
		hRets = a.mergeLanes(hRets, stripTag(hEx.rets, tagViaReturn))
		// 分支内抛出（未被内层接住）也成为未处理抛出车道。
		throws = append(throws, hThrows...)
	}

	var normalIn [][]lane
	if len(body) > 0 {
		normalIn = append(normalIn, retag(body, tagNormalBody))
	}
	normalIn = append(normalIn, retag(hNormal, tagHandlerEnd))
	if len(throws) > 0 {
		// 未处理抛出车道：assigned 为 try 前状态，life 为抛出前缀 life。
		var uncaught []lane
		for _, th := range throws {
			base := preAssignByTrace(in, th.traces)
			uncaught = append(uncaught, lane{
				assign: base, life: th.life.clone(),
				traces: appendTag(th.traces, tagUncaught),
			})
		}
		normalIn = append(normalIn, uncaught)
	}

	breakIn := a.mergeLanes(stripTag(bodyEx.breaks, tagViaBreak), hBreaks)
	retIn := a.mergeLanes(stripTag(bodyEx.rets, tagViaReturn), hRets)

	out := exits{}
	var after []lane
	runCleanup := func(ls []lane, fall string) {
		if len(ls) == 0 {
			return
		}
		tagged := forkLanes(ls, tagCleanup)
		var outerThrows []lane
		cn, ce := a.analyzeSeq(s.Cleanup, tagged, &outerThrows)
		for _, l := range cn {
			switch fall {
			case tagBreak:
				out.breaks = a.mergeLanes(out.breaks, retag([]lane{l}, tagBreak))
			case tagReturn:
				out.rets = a.mergeLanes(out.rets, retag([]lane{l}, tagReturn))
			default:
				after = a.mergeLanes(after, []lane{l})
			}
		}
		out.breaks = a.mergeLanes(out.breaks, ce.breaks)
		out.rets = a.mergeLanes(out.rets, ce.rets)
		// 清理区域中途抛出：逃逸到外层保护结构（外层按进入外层 try 前状态）。
		out.throws = a.mergeLanes(out.throws, outerThrows)
	}
	runCleanup(a.mergeLanes(normalIn...), "normal")
	runCleanup(breakIn, tagBreak)
	runCleanup(retIn, tagReturn)
	return after, out
}
