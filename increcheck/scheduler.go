package increcheck

// scheduler.go：失效判定 + 检查调度 + 结果缓存入口。
//
// 串行等价：一把互斥锁串行化全部编辑与查询；查询在锁内同一瞬间
// 取出结果与依据版本，杜绝跨版本拼接。编辑策略选择「调度进行中拒绝」：
// 编辑在锁内同步完成整轮调度，被拒绝只可能发生在检查器回调重入
// 提交编辑时，此时返回 ErrBusy，不改任何依赖、版本与缓存。
//
// 每轮签名失效调度：
//  1. 初始失效集合 seeds（被编辑/删除的签名自身）。
//  2. 沿旧的签名反向边求精确闭包；实现依赖的反向边只用于定位
//     失效的实现检查，不参与签名闭包（实现变化不影响他人）。
//  3. 在闭包子图上求 SCC：互相引用签名的声明构成一个整组。
//  4. 组按「被依赖者优先」拓扑序处理；单成员与多成员统一走整组事务：
//     组内全部重检，结果要么全部采纳要么全部不采纳；任一成员报错，
//     该成员结果即错误态并递增版本，下游按签名已变化处理。
//  5. 逐成员用重检后的实际签名结果与旧结果比较：完全相同则版本不
//     递增、结果不替换、传递在此截断（早停的唯一依据是实际结果比较）。
//  6. 最后处理实现检查：依据版本逐一直等则沿用，否则重检。

import "sync"

// Scheduler 是增量类型检查调度子系统的对外入口。
type Scheduler struct {
	mu      sync.Mutex
	reg     *Registry
	checker *Checker
	deps    *DepGraph
	cache   *Cache
	stats   Stats
	log     Logger
	running bool
	last    *EditOutcome
	// onCheckID 在每次签名/实现检查读取声明 id 后调用；
	// 用于测试在「调度进行中」重入提交编辑，验证 ErrBusy 零副作用。
	onCheckID func(kind, id string)
}

func NewScheduler() *Scheduler {
	return NewSchedulerWithLogger(NopLogger{})
}

func NewSchedulerWithLogger(log Logger) *Scheduler {
	return &Scheduler{
		reg:     NewRegistry(),
		checker: NewChecker(),
		deps:    NewDepGraph(),
		cache:   NewCache(),
		log:     log,
	}
}

// Stats 返回统计视图快照。
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// LastOutcome 返回最近一次生效编辑的调度明细。
func (s *Scheduler) LastOutcome() EditOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return EditOutcome{}
	}
	return *s.last
}

// Add 添加一条全新声明并完成首轮检查。
func (s *Scheduler) Add(id string, d Declaration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(id, d)
}

func (s *Scheduler) addLocked(id string, d Declaration) error {
	if s.rejectIfBusy("add", id) {
		return &EditError{ErrBusy}
	}
	if err := s.reg.add(id, d); err != nil {
		s.log.Logf("ADD %q REJECTED %v", id, err)
		return err
	}
	// 「不存在 -> 存在」是签名结果的实际变化，版本立即递增一次，
	// 之后的检查在新版本上建立依据（重新添加由此触发二次失效）。
	s.reg.bump(id)
	// 先对新声明做一次检查以登记其依赖边（依赖可能尚不存在，
	// 结果即未定义错误态），使未来被依赖者出现时能经反向边精确定位，
	// 失效判定因此从不扫描无关声明。
	initial := s.checkOneSig(id)
	s.cache.putSig(id, initial)
	s.deps.recordSig(id, initial.basisIDs())
	// 同样立即检查一次实现以登记「实现 -> 签名」依赖边；
	// 结果（可能是未定义错误）缓存，后续沿用按依据版本判定。
	s.checkOneImpl(id, &EditOutcome{})

	seeds := map[string]struct{}{id: {}}
	// 新签名出现：所有直接引用它的既有签名检查依据立即失配，
	// 经反向边逐层纳入同一轮调度（含删除后重新添加的二次失效）。
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, dep := range s.deps.sigDependents(cur) {
			if _, ok := seeds[dep]; !ok {
				seeds[dep] = struct{}{}
				stack = append(stack, dep)
			}
		}
	}
	oc := s.runSigSchedule(seeds, "add:"+id)
	implExtra := map[string]struct{}{id: {}}
	// 新签名出现也使「实现直接依赖它」的检查依据失配；
	// 经实现反向边收集一层（实现依赖不向外传递）。
	for _, owner := range s.deps.implDependents(id) {
		implExtra[owner] = struct{}{}
	}
	s.scheduleImpls(oc, implExtra)
	s.finishEdit(oc)
	s.log.Logf("ADD %q ok changed=%v early=%v impl=%v reuseImpl=%v",
		id, oc.ChangedSigs, oc.StoppedEarly, oc.RecheckedImpls, oc.ReusedImpls)
	return nil
}

// EditSig 编辑签名文本；空操作零副作用，否则触发签名失效调度。
func (s *Scheduler) EditSig(id, sig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editSigLocked(id, sig)
}

func (s *Scheduler) editSigLocked(id, sig string) error {
	if s.rejectIfBusy("edit_sig", id) {
		return &EditError{ErrBusy}
	}
	if err := s.reg.editSig(id, sig); err != nil {
		if isErrCode(err, ErrNoOp) {
			s.stats.NoOps++
		}
		s.log.Logf("EDIT_SIG %q NO-EFFECT %v", id, err)
		return err
	}
	oc := s.runSigSchedule(map[string]struct{}{id: {}}, "sig:"+id)
	s.scheduleImpls(oc, map[string]struct{}{id: {}})
	s.finishEdit(oc)
	s.log.Logf("EDIT_SIG %q ok changed=%v early=%v impl=%v reuseImpl=%v",
		id, oc.ChangedSigs, oc.StoppedEarly, oc.RecheckedImpls, oc.ReusedImpls)
	return nil
}

// EditImpl 编辑实现文本：只使自身的实现检查失效，绝不外溢。
func (s *Scheduler) EditImpl(id, impl string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editImplLocked(id, impl)
}

func (s *Scheduler) editImplLocked(id, impl string) error {
	if s.rejectIfBusy("edit_impl", id) {
		return &EditError{ErrBusy}
	}
	if err := s.reg.editImpl(id, impl); err != nil {
		if isErrCode(err, ErrNoOp) {
			s.stats.NoOps++
		}
		s.log.Logf("EDIT_IMPL %q NO-EFFECT %v", id, err)
		return err
	}
	oc := &EditOutcome{}
	s.checkOneImpl(id, oc)
	s.finishEdit(oc)
	s.log.Logf("EDIT_IMPL %q ok impl=%v reuseImpl=%v", id, oc.RecheckedImpls, oc.ReusedImpls)
	return nil
}

// Delete 等同把签名改为「不存在」，走完整签名失效与传递流程。
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(id)
}

func (s *Scheduler) deleteLocked(id string) error {
	if s.rejectIfBusy("delete", id) {
		return &EditError{ErrBusy}
	}
	if err := s.reg.delete(id); err != nil {
		s.log.Logf("DELETE %q NO-EFFECT %v", id, err)
		return err
	}
	oc := s.runSigSchedule(map[string]struct{}{id: {}}, "delete:"+id)
	s.scheduleImpls(oc, map[string]struct{}{id: {}})
	s.finishEdit(oc)
	s.log.Logf("DELETE %q ok changed=%v early=%v impl=%v reuseImpl=%v",
		id, oc.ChangedSigs, oc.StoppedEarly, oc.RecheckedImpls, oc.ReusedImpls)
	return nil
}

// SigResult 查询签名结果与同一瞬间的依据；按需检查从未检查的声明。
func (s *Scheduler) SigResult(id string) SigEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.cache.getSig(id); ok {
		s.stats.Reused++
		s.log.Logf("QUERY_SIG %q REUSE basis={%s}", id, formatBasis(e.Basis))
		return e
	}
	e := s.checkOneSig(id)
	s.cache.putSig(id, e)
	s.stats.RecheckedSigs++
	s.log.Logf("QUERY_SIG %q MISS result={%s} basis={%s}",
		id, formatSig(e.Result), formatBasis(e.Basis))
	return e
}

// ImplResult 查询实现结果与同一瞬间的依据；依据失配或缺失则重检。
func (s *Scheduler) ImplResult(id string) ImplEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.cache.getImpl(id); ok && basisValid(e.Basis, s.reg) {
		s.stats.Reused++
		s.log.Logf("QUERY_IMPL %q REUSE basis={%s}", id, formatBasis(e.Basis))
		return e
	}
	oc := &EditOutcome{}
	e := s.checkOneImpl(id, oc)
	s.log.Logf("QUERY_IMPL %q MISS err=%v basis={%s}",
		id, e.Result.Err, formatBasis(e.Basis))
	return e
}

func (s *Scheduler) rejectIfBusy(op, id string) bool {
	if !s.running {
		return false
	}
	s.stats.Rejected++
	s.log.Logf("%s %q REJECTED busy", op, id)
	return true
}

func (s *Scheduler) finishEdit(oc *EditOutcome) {
	s.stats.Edits++
	s.stats.RecheckedSigs += int64(len(oc.RecheckedSigs))
	s.stats.RecheckedImpls += int64(len(oc.RecheckedImpls))
	s.stats.StoppedEarly += int64(len(oc.StoppedEarly) + len(oc.SavedByEarlyStop))
	s.stats.Reused += int64(len(oc.ReusedImpls))
	cp := *oc
	s.last = &cp
}

// runSigSchedule 执行一轮签名失效传播与 SCC 整组重检。
func (s *Scheduler) runSigSchedule(seeds map[string]struct{}, tag string) *EditOutcome {
	oc := &EditOutcome{}
	s.running = true
	defer func() { s.running = false }()

	// 先按种子声明的当前源码刷新其「签名 -> 签名」出边：
	// 一次编辑可能让签名新增/删除引用，新引用必须参与本轮的 SCC 分组
	// 与失效闭包（例如编辑后与某现存声明形成互相引用的循环组）。
	for id := range seeds {
		if d, ok := s.reg.get(id); ok {
			s.deps.recordSig(id, parseRefs(d.SigText))
		} else {
			s.deps.recordSig(id, nil)
		}
	}
	// 闭包与 SCC 都在更新后的图上求：新出边引入的反向依赖者自然进入
	// 受影响区域（例如新增引用与某现存声明形成互相引用的循环组）。
	// 波次传播仍只把「实际变化」送到下游，早停语义不变。
	affected := s.sigClosure(seeds)
	nodes := make([]string, 0, len(affected))
	for id := range affected {
		nodes = append(nodes, id)
	}
	groups := topoGroups(s.deps, sigSCC(s.deps, nodes))
	s.log.Logf("SCHEDULE %s groups=%v", tag, groups)
	// roundResults：本轮已经采纳的组的最新签名结果。
	// 后续组读取签名时必须优先看到它，而不是旧缓存里的错误态——
	// 否则一个已在本轮恢复正常的依赖会被误读为仍出错。
	roundResults := map[string]SigResult{}

	// 波次传播：只在拓扑序上重检被「变化波」实际送达的组。
	// 组在其全部前驱都处理后才能裁决；收到变化波（任一前驱最终变化、
	// 或自身是种子）才整组重检；否则整个组免检——变化已在早停处截断。
	groupOf := map[string]int{}
	indeg := make([]int, len(groups))
	succ := make([][]int, len(groups))
	for gi, members := range groups {
		for _, m := range members {
			groupOf[m] = gi
		}
	}
	for gi, members := range groups {
		in := map[int]struct{}{}
		for _, m := range members {
			for _, dep := range s.deps.sigDepsOf(m) {
				if gj, ok := groupOf[dep]; ok && gj != gi {
					in[gj] = struct{}{}
				}
			}
		}
		indeg[gi] = len(in)
		for gj := range in {
			succ[gj] = append(succ[gj], gi)
		}
	}
	seedGroup := map[int]struct{}{}
	for id := range seeds {
		if gi, ok := groupOf[id]; ok {
			seedGroup[gi] = struct{}{}
		}
	}

	// blockedBy：拓扑序计数器，前驱处理完即减，与是否变化无关。
	// dirtyBy：只统计「实际发生变化、且尚未处理」的前驱数量；
	// 早停前驱不贡献脏计数，于是被它挡住的下游永不裁决为重检。
	blockedBy := append([]int(nil), indeg...)
	dirtyBy := make([]int, len(groups))
	handled := map[int]bool{}
	reached := map[int]bool{} // 被种子或实际变化波送达的组
	for gi := range seedGroup {
		reached[gi] = true
	}
	var todo []int
	for gi, d := range blockedBy {
		if d == 0 {
			todo = append(todo, gi)
		}
	}
	sortGroupsReady(groups, todo)

	for len(todo) > 0 {
		gi := todo[0]
		todo = todo[1:]
		_, seeded := seedGroup[gi]
		dirty := seeded || dirtyBy[gi] > 0
		if dirty {
			reached[gi] = true
		}
		handled[gi] = true
		changedHere := false
		if dirty {
			s.recheckSigGroup(groups[gi], oc, roundResults)
			for _, m := range groups[gi] {
				roundResults[m] = s.cache.getSigResult(m)
				if contains(oc.ChangedSigs, m) {
					changedHere = true
				}
			}
		}
		var fresh []int
		for _, gj := range succ[gi] {
			if changedHere {
				dirtyBy[gj]++
				reached[gj] = true
			}
			blockedBy[gj]--
			if blockedBy[gj] == 0 && !handled[gj] {
				fresh = append(fresh, gj)
			}
		}
		todo = append(todo, fresh...)
		sortGroupsReady(groups, todo)
	}
	// 所有拓扑序处理结束后，仍未被变化波送达的组：其上游在早停处
	// 截断了传递，它们既未重检、也不是早停点，计入「免于重检」。
	for gi, members := range groups {
		if !reached[gi] {
			for _, m := range members {
				oc.SavedByEarlyStop = append(oc.SavedByEarlyStop, m)
				s.log.Logf("SIG %s SAVED by upstream early stop", m)
			}
		}
	}
	return oc
}

// recheckSigGroup 把一个 SCC（或单成员组）作为整体重检并整体采纳：
// 先全部检查取结果，再逐成员与旧结果比较；任一处报错即该成员结果
// 为错误态（变化），依赖整组的下游按签名已变化处理。
func (s *Scheduler) recheckSigGroup(members []string, oc *EditOutcome, roundResults map[string]SigResult) {
	inGroup := map[string]struct{}{}
	for _, m := range members {
		inGroup[m] = struct{}{}
	}
	// 读取视图 = 本轮已采纳结果 > 组内暂存 > 缓存 > 登记。
	// 初值只预置组外可确定的事实（成员不存在、或直接引用组外缺失标识）；
	// 组内引用先视为正常，再迭代到不动点：错误沿真实错误路径传播，
	// 健康自循环（无外部缺失）保持正常，且旧缓存错误不会残留。
	staged := map[string]SigResult{}
	for _, m := range members {
		if _, exists := s.reg.get(m); !exists {
			staged[m] = SigResult{Present: false}
			continue
		}
	}
	// 未预置的组内成员先放「正常」暂存值，使迭代可启动。
	for _, m := range members {
		if _, ok := staged[m]; !ok {
			if d, exists := s.reg.get(m); exists {
				staged[m] = SigResult{Present: true, Text: d.SigText}
			}
		}
	}
	// 迭代重算组内结果直至稳定（有界：成员数次内必然不动）。
	for {
		changed := false
		next := map[string]SigResult{}
		for _, m := range members {
			e := s.checkOneSigStaged(m, s.mergeStaged(roundResults, staged))
			next[m] = e.Result
		}
		for _, m := range members {
			if !staged[m].equal(next[m]) {
				changed = true
			}
		}
		staged = next
		if !changed {
			break
		}
	}
	entries := map[string]SigEntry{}
	for _, m := range members {
		entries[m] = s.checkOneSigStaged(m, s.mergeStaged(roundResults, staged))
	}
	// 先在「不递增」的前提下逐成员裁决变化与否，并完成本组全部递增；
	// 之后再统一在新版本快照上重建依据并落盘——保证组内相互引用的
	// 依据版本彼此一致、且与当前登记逐一直等。
	identical := map[string]bool{}
	for _, m := range members {
		oc.RecheckedSigs = append(oc.RecheckedSigs, m)
		old, hadOld := s.cache.getSig(m)
		if hadOld && old.Result.equal(entries[m].Result) {
			identical[m] = true
		} else {
			s.reg.bump(m)
		}
	}
	for _, m := range members {
		newEntry := entries[m]
		fresh := newEntry.basisIDs()
		newEntry.Basis = makeBasis(s.reg, fresh)
		if identical[m] {
			// 早停：实际签名结果相同，版本未递增；依据对齐到当前版本。
			s.cache.putSig(m, newEntry)
			s.deps.recordSig(m, fresh)
			oc.StoppedEarly = append(oc.StoppedEarly, m)
			s.log.Logf("SIG %s EARLY-STOP identical={%s}", m, formatSig(newEntry.Result))
			continue
		}
		s.cache.putSig(m, newEntry)
		s.deps.recordSig(m, fresh)
		oc.ChangedSigs = append(oc.ChangedSigs, m)
		s.log.Logf("SIG %s CHANGED v%d result={%s} basis={%s}",
			m, s.reg.sigVersion(m), formatSig(newEntry.Result), formatBasis(newEntry.Basis))
	}
}

// sigClosure 沿旧签名反向边从 seeds 求精确失效闭包，
// 只走签名 -> 签名边；工作量正比于失效区域，不触碰无关声明。
func (s *Scheduler) sigClosure(seeds map[string]struct{}) map[string]struct{} {
	seen := map[string]struct{}{}
	stack := make([]string, 0, len(seeds))
	for id := range seeds {
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		for _, d := range s.deps.sigDependents(id) {
			if _, ok := seen[d]; !ok {
				stack = append(stack, d)
			}
		}
	}
	return seen
}

// scheduleImpls 处理签名变化波及的实现检查。
// 候选 = 直接编辑的现存声明，加上实现检查直接依赖任一实际变化签名的
// 声明；只查反向边，不随程序规模增长。旧结果依据版本逐一直等则沿用。
func (s *Scheduler) scheduleImpls(oc *EditOutcome, extra map[string]struct{}) {
	cand := map[string]struct{}{}
	for id := range extra {
		if _, ok := s.reg.get(id); ok {
			cand[id] = struct{}{}
		}
	}
	// 种子签名 = 被直接编辑的声明 + 本轮所有实际重检过的签名
	// （含早停者）。经实现反向边收集一层候选：实现依赖不传递，
	// 因此只收集一层，工作量不触及无关声明。
	seedSigs := map[string]struct{}{}
	for id := range extra {
		seedSigs[id] = struct{}{}
	}
	for _, id := range oc.RecheckedSigs {
		seedSigs[id] = struct{}{}
	}
	for sig := range seedSigs {
		for _, owner := range s.deps.implDependents(sig) {
			if _, ok := s.reg.get(owner); ok {
				cand[owner] = struct{}{}
			}
		}
	}
	for _, id := range sortedSet(cand) {
		if e, ok := s.cache.getImpl(id); ok && basisValid(e.Basis, s.reg) {
			oc.ReusedImpls = append(oc.ReusedImpls, id)
			s.stats.Reused++
			s.log.Logf("IMPL %s REUSE basis={%s}", id, formatBasis(e.Basis))
			continue
		}
		s.checkOneImpl(id, oc)
	}
}

// depRecorder 是检查期间被动收集签名读取的读取器（依赖是集合）。
type depRecorder struct{ seen map[string]struct{} }

func (d *depRecorder) record(id string) {
	if d.seen == nil {
		d.seen = map[string]struct{}{}
	}
	d.seen[id] = struct{}{}
}

func (d *depRecorder) refs() []string {
	out := make([]string, 0, len(d.seen))
	for id := range d.seen {
		out = append(out, id)
	}
	return sortedStrings(out)
}

// sigReader 构造签名读取器：读取即记依赖，并返回当前可用结果。
// staged 提供本轮组重检预置的错误事实；否则查缓存；缓存缺失且声明
// 存在时按当前登记即时重算（不写缓存）；不存在则返回 absent。
func (s *Scheduler) sigReader(rec *depRecorder, staged map[string]SigResult) SigReader {
	return func(id string) SigResult {
		rec.record(id)
		r, _ := s.lookupSigForRound(id, nil, staged)
		return r
	}
}

// lookupSigForRound 解析一次签名读取：round > staged > cache > 即时重算。
func (s *Scheduler) lookupSigForRound(id string, round, staged map[string]SigResult) (SigResult, []string) {
	if r, ok := round[id]; ok {
		return r, nil
	}
	if r, ok := staged[id]; ok {
		return r, nil
	}
	if e, ok := s.cache.getSig(id); ok {
		return e.Result, e.basisIDs()
	}
	if _, ok := s.reg.get(id); !ok {
		return SigResult{Present: false}, nil
	}
	// 现存但尚未缓存（正处在其自身检查途中的自引用等）：
	// 存在性已确定，按当前登记即时重算，并防自引用递归。
	sub := &depRecorder{}
	guard := map[string]SigResult{}
	for k, v := range s.mergeStaged(round, staged) {
		guard[k] = v
	}
	if _, looping := guard[id]; !looping {
		if d, ok := s.reg.get(id); ok {
			guard[id] = SigResult{Present: true, Text: d.SigText}
		}
	}
	r := s.checker.CheckSig(s.reg, id, s.sigReader(sub, guard))
	return r, sub.refs()
}

// mergeStaged 返回一个合并视图：round 优先于 staged。
func (s *Scheduler) mergeStaged(round, staged map[string]SigResult) map[string]SigResult {
	if len(round) == 0 {
		return staged
	}
	m := map[string]SigResult{}
	for k, v := range staged {
		m[k] = v
	}
	for k, v := range round {
		m[k] = v
	}
	return m
}

func (s *Scheduler) checkOneSig(id string) SigEntry {
	return s.checkOneSigStaged(id, nil)
}

// checkOneSigStaged 执行一次签名检查，staged 提供组内预置结果。
func (s *Scheduler) checkOneSigStaged(id string, staged map[string]SigResult) SigEntry {
	rec := &depRecorder{}
	reader := s.sigReader(rec, staged)
	read := func(ref string) SigResult {
		// 自引用：声明在检查开始时已确定存在；读取自身签名返回
		// 当前暂存/缓存/一个不含错误的自值，绝不递归重算。
		if ref == id {
			rec.record(ref)
			// 自引用读取的是「本声明存在」这一事实：只要声明现存，
			// 自读永远是一个存在且无错误的自值（错误只可能来自其它引用）。
			if d, ok := s.reg.get(ref); ok {
				return SigResult{Present: true, Text: d.SigText}
			}
			return SigResult{Present: false}
		}
		return reader(ref)
	}
	res := s.checker.CheckSig(s.reg, id, read)
	if s.onCheckID != nil {
		s.onCheckID("sig", id)
	}
	refs := rec.refs()
	return SigEntry{Result: res, Basis: makeBasis(s.reg, refs)}
}

// checkOneImpl 执行一次实现检查，立即在当前版本快照上建立依据并缓存。
func (s *Scheduler) checkOneImpl(id string, oc *EditOutcome) ImplEntry {
	rec := &depRecorder{}
	res := s.checker.CheckImpl(s.reg, id, s.sigReader(rec, nil))
	if s.onCheckID != nil {
		s.onCheckID("impl", id)
	}
	refs := rec.refs()
	e := ImplEntry{Result: res, Basis: makeBasis(s.reg, refs)}
	s.deps.recordImpl(id, refs)
	s.cache.putImpl(id, e)
	oc.RecheckedImpls = append(oc.RecheckedImpls, id)
	s.log.Logf("IMPL %s RECHECK err=%v basis={%s}", id, res.Err, formatBasis(e.Basis))
	return e
}

// EditOutcome 描述一次编辑后实际发生的调度。
type EditOutcome struct {
	ChangedSigs      []string // 重检后实际变化、递增了版本的签名
	RecheckedSigs    []string // 实际执行签名检查的声明（含早停者）
	RecheckedImpls   []string // 实际执行实现检查的声明
	StoppedEarly     []string // 重检签名相同、传递在此截断的声明
	SavedByEarlyStop []string // 因上游早停而根本未重检的声明
	ReusedImpls      []string // 依据版本全部一致而沿用的实现检查
}

func isErrCode(err error, code ErrCode) bool {
	if e, ok := err.(*EditError); ok {
		return e.Code == code
	}
	return false
}

func dedup(xs []string) []string {
	seen := map[string]struct{}{}
	out := xs[:0]
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return sortedStrings(out)
}
