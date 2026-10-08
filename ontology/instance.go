package ontology

import (
	"sort"
	"sync"
	"time"
)

// instanceState 是实例对外可观察状态的快照，用于验证失败路径无副作用。
type instanceState struct {
	Version   uint64
	Props     map[string]any
	UpdatedAt time.Time
}

// instance 是单个对象实例的仲裁器与数据载体。
// 全部字段仅在持有 mu 时访问，因此所有提交都在同一把锁下串行发生，
// 提交顺序天然构成等价串行顺序。
type instance struct {
	mu   sync.Mutex
	cond *sync.Cond

	version   uint64
	props     map[string]any
	updatedAt time.Time

	// seq 是实例级逻辑时钟：仅在请求登记到达与提交时递增。
	// 它是优先级计算唯一使用的时间来源，与墙钟无关。
	seq uint64

	// gen 是状态代际计数器：仅在提交时递增。被挤出的请求据此阻塞
	// 等待状态变化：每次被唤醒都意味着有竞争者提交离开，
	// 这是失败次数上界证明的关键。
	gen uint64

	// waiters 是当前处于等待或重试中的竞争者集合，
	// 值为服务端权威票据（防止调用方重置优先级依据）。
	waiters map[RequestID]Ticket
	// lastReq 记录每个等待者最近一次尝试携带的请求负载。
	lastReq map[RequestID]*WriteRequest

	// log 是完整的裁定决策日志，可用于重放核验。
	log []Decision
}

func newInstance() *instance {
	inst := &instance{
		props:   make(map[string]any),
		waiters: make(map[RequestID]Ticket),
		lastReq: make(map[RequestID]*WriteRequest),
	}
	inst.cond = sync.NewCond(&inst.mu)
	return inst
}

// allocSeq 分配一个逻辑序号并推进实例逻辑时钟。
func (inst *instance) allocSeq() uint64 {
	s := inst.seq
	inst.seq++
	return s
}

// applyLocked 在持锁状态下应用补丁并推进版本与逻辑时钟。
// 只有获得提交权的请求才会走到这里；任何失败路径都不会调用它。
func (inst *instance) applyLocked(patch map[string]any) {
	for k, v := range patch {
		inst.props[k] = v
	}
	inst.version++
	inst.updatedAt = time.Now()
	inst.seq++
	inst.gen++
	inst.cond.Broadcast()
}

// snapshotLocked 返回实例对外可观察状态的快照。
func (inst *instance) snapshotLocked() instanceState {
	props := make(map[string]any, len(inst.props))
	for k, v := range inst.props {
		props[k] = v
	}
	return instanceState{Version: inst.version, Props: props, UpdatedAt: inst.updatedAt}
}

// headLocked 返回当前等待集中优先级最高的请求（裁定胜者候选）。
// 等待者之间的相对优先级与逻辑时钟取值无关
// （score 差 = (failures - arrivalSeq) 之差，now 相互抵消），
// 因此该顺序只在某个等待者失败次数增加时才会变化。
// 返回扫描执行的比较次数。
func (inst *instance) headLocked() (RequestID, int) {
	var head RequestID
	var headKey priorityKey
	comparisons := 0
	first := true
	for id, t := range inst.waiters {
		k := keyOf(id, t, inst.seq)
		if first || headKey.less(k) {
			head, headKey = id, k
		}
		if !first {
			comparisons++
		}
		first = false
	}
	return head, comparisons
}

// contendersLocked 返回等待集的快照（按优先级从高到低排序），用于决策日志。
func (inst *instance) contendersLocked() []ContenderSnapshot {
	contenders := make([]ContenderSnapshot, 0, len(inst.waiters))
	for id, t := range inst.waiters {
		contenders = append(contenders, ContenderSnapshot{
			ID:         id,
			ArrivalSeq: t.ArrivalSeq,
			Failures:   t.Failures,
			Score:      t.Score(inst.seq),
		})
	}
	sort.Slice(contenders, func(i, j int) bool {
		ki := priorityKey{score: contenders[i].Score, arrival: contenders[i].ArrivalSeq, id: contenders[i].ID}
		kj := priorityKey{score: contenders[j].Score, arrival: contenders[j].ArrivalSeq, id: contenders[j].ID}
		return kj.less(ki)
	})
	return contenders
}

// attemptResult 是一次尝试的结果。
type attemptResult struct {
	Outcome Outcome
	Version uint64 // 仅 OutcomeCommitted 时有效
	Ticket  Ticket // 裁定后的权威票据
	Score   uint64 // 裁定时该请求的分数
	// DecisionSeq 是本次尝试对应裁定决策的逻辑时钟值。
	DecisionSeq uint64
	// Generation 是裁定时的状态代际，被挤出的请求据此等待状态变化。
	Generation uint64
}

// attempt 执行一次写入尝试。它是仲裁器的唯一入口，
// 每次调用恰好产生一条 Decision 日志并返回一个互斥的结果。
// 判定顺序：先做优先级裁定（是否为当前最高优先级等待者），
// 后做重试上限判定；因此被挤出的那次尝试绝不会同时被计为触及上限。
func (inst *instance) attempt(req *WriteRequest) attemptResult {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if t, ok := inst.waiters[req.ID]; ok {
		// 已登记请求的重试：以服务端权威票据为准，防止调用方重置。
		req.Ticket = t
		inst.lastReq[req.ID] = req

		head, comparisons := inst.headLocked()
		if head == req.ID {
			// 优先级裁定通过：获得提交权。即使失败次数已达上限，
			// 裁定优胜者仍可完成提交（裁定先于上限判定）。
			if req.BaseVersion == inst.version {
				now := inst.seq
				contenders := inst.contendersLocked()
				inst.applyLocked(req.Patch)
				delete(inst.waiters, req.ID)
				delete(inst.lastReq, req.ID)
				committed := inst.version
				inst.logDecision(Decision{
					Seq:              now,
					Kind:             DecideCommit,
					Attempter:        req.ID,
					Outcome:          OutcomeCommitted,
					Version:          committed,
					Ticket:           t,
					MaxRetries:       req.MaxRetries,
					AdmissionChecks:  1,
					Contenders:       contenders,
					Head:             req.ID,
					RoundComparisons: comparisons,
				})
				return attemptResult{Outcome: OutcomeCommitted, Version: committed, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
			}
			// 裁定优胜但版本已过期：写冲突，失败次数 +1，可立即重试。
			t.Failures++
			inst.waiters[req.ID] = t
			req.Ticket = t
			now := inst.seq
			inst.logDecision(Decision{
				Seq:              now,
				Kind:             DecideConflict,
				Attempter:        req.ID,
				Outcome:          OutcomeConflict,
				Version:          inst.version,
				Ticket:           t,
				MaxRetries:       req.MaxRetries,
				AdmissionChecks:  1,
				Contenders:       inst.contendersLocked(),
				Head:             req.ID,
				RoundComparisons: comparisons,
			})
			return attemptResult{Outcome: OutcomeConflict, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
		}

		// 优先级裁定落败。重试上限判定发生在裁定之后：
		// 若失败次数此前已达上限，本次调度判定为耗尽（最终失败）；
		// 否则计入一次被挤出，本次结果仅为 Preempted，绝不与耗尽混淆。
		// 耗尽的是非最高优先级者，其离开不改变最高优先级者，
		// 因此无需唤醒其他等待者。
		if t.Failures > req.MaxRetries {
			delete(inst.waiters, req.ID)
			delete(inst.lastReq, req.ID)
			now := inst.seq
			inst.logDecision(Decision{
				Seq:             now,
				Kind:            DecideExhausted,
				Attempter:       req.ID,
				Outcome:         OutcomeExhausted,
				Version:         inst.version,
				Ticket:          t,
				MaxRetries:      req.MaxRetries,
				AdmissionChecks: 1,
			})
			return attemptResult{Outcome: OutcomeExhausted, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
		}
		t.Failures++
		inst.waiters[req.ID] = t
		req.Ticket = t
		now := inst.seq
		inst.logDecision(Decision{
			Seq:              now,
			Kind:             DecidePreempted,
			Attempter:        req.ID,
			Outcome:          OutcomePreempted,
			Version:          inst.version,
			Ticket:           t,
			MaxRetries:       req.MaxRetries,
			AdmissionChecks:  1,
			Contenders:       inst.contendersLocked(),
			Head:             head,
			RoundComparisons: comparisons,
		})
		return attemptResult{Outcome: OutcomePreempted, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
	}

	// 新到达请求的准入判定：只需检查等待集是否为空。
	// 不变式：任何已登记等待者（Failures >= 1）严格优先于任何新到达
	// 请求（Failures == 0 且到达更晚），因此新到达请求相对全部等待者
	// 的优先级判定只需 O(1) 的空集检查，与竞争者总数无关。
	if len(inst.waiters) == 0 {
		if req.BaseVersion == inst.version {
			now := inst.seq
			inst.applyLocked(req.Patch)
			inst.logDecision(Decision{
				Seq:             now,
				Kind:            DecideFastCommit,
				Attempter:       req.ID,
				Outcome:         OutcomeCommitted,
				Version:         inst.version,
				Ticket:          req.Ticket,
				MaxRetries:      req.MaxRetries,
				AdmissionChecks: 1,
			})
			return attemptResult{Outcome: OutcomeCommitted, Version: inst.version, Ticket: req.Ticket, DecisionSeq: now, Generation: inst.gen}
		}
		// 写冲突：登记进入等待集并计入一次失败。
		t := Ticket{ArrivalSeq: inst.allocSeq(), Failures: 1}
		inst.waiters[req.ID] = t
		inst.lastReq[req.ID] = req
		req.Ticket = t
		now := inst.seq
		inst.logDecision(Decision{
			Seq:              now,
			Kind:             DecideConflict,
			Attempter:        req.ID,
			Outcome:          OutcomeConflict,
			Version:          inst.version,
			Ticket:           t,
			MaxRetries:       req.MaxRetries,
			AdmissionChecks:  1,
			Contenders:       inst.contendersLocked(),
			Head:             req.ID,
			RoundComparisons: 0,
		})
		return attemptResult{Outcome: OutcomeConflict, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
	}

	// 存在竞争者：新到达请求登记后立即参与裁定。
	// 由不变式可知其必然落败，结果为被挤出（失败次数记 1）。
	t := Ticket{ArrivalSeq: inst.allocSeq(), Failures: 1}
	inst.waiters[req.ID] = t
	inst.lastReq[req.ID] = req
	req.Ticket = t
	head, comparisons := inst.headLocked()
	now := inst.seq
	inst.logDecision(Decision{
		Seq:              now,
		Kind:             DecidePreempted,
		Attempter:        req.ID,
		Outcome:          OutcomePreempted,
		Version:          inst.version,
		Ticket:           t,
		MaxRetries:       req.MaxRetries,
		AdmissionChecks:  1,
		Contenders:       inst.contendersLocked(),
		Head:             head,
		RoundComparisons: comparisons,
	})
	return attemptResult{Outcome: OutcomePreempted, Ticket: t, Score: t.Score(now), DecisionSeq: now, Generation: inst.gen}
}

// waitForChange 阻塞等待实例状态发生变化（版本推进或等待集变化），
// 用于被挤出后的重试等待，避免空转。since 为被挤出时观察到的代际。
func (inst *instance) waitForChange(since uint64) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for inst.gen == since {
		inst.cond.Wait()
	}
}

// logDecision 追加一条裁定决策日志。
func (inst *instance) logDecision(d Decision) {
	inst.log = append(inst.log, d)
}
