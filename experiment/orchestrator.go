// Package experiment 实现带冷却隔离与世代重盐的互斥实验层流量编排器。
//
// 桶 0 到 H-1 永久保留为对照组；桶 H 到 B-1 处于空闲、占用、冷却三态之一。
// 用户哈希 h 经 (h + g*P) mod B 映射到桶，从而映射到所属实验。
package experiment

import (
	"errors"
	"fmt"
	"sync"
)

// Reason 区分操作被拒绝的原因。
type Reason int

const (
	// ReasonInvalidParams 参数非法（id 为空、n/n2 越界、now 越界）。
	ReasonInvalidParams Reason = iota
	// ReasonExperimentExists Claim 时实验已存在。
	ReasonExperimentExists
	// ReasonExperimentNotExists Resize/Release 时实验不存在。
	ReasonExperimentNotExists
	// ReasonClockRollback now 小于已被接受操作的最大 now。
	ReasonClockRollback
	// ReasonGenerationExhausted 世代 g 已达上限时的 Reshuffle。
	ReasonGenerationExhausted
	// ReasonInsufficientCapacity 找不到满足条件的连续桶段。
	ReasonInsufficientCapacity
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidParams:
		return "invalid params"
	case ReasonExperimentExists:
		return "experiment already exists"
	case ReasonExperimentNotExists:
		return "experiment not exists"
	case ReasonClockRollback:
		return "clock rollback"
	case ReasonGenerationExhausted:
		return "generation exhausted"
	case ReasonInsufficientCapacity:
		return "insufficient capacity"
	}
	return "unknown"
}

// Error 是被拒绝操作返回的错误，Reason 可区分拒绝原因。
type Error struct {
	Reason Reason
	Op     string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("experiment: %s rejected: %s (%s)", e.Op, e.Reason, e.Detail)
}

// ReasonOf 从 err 中提取拒绝原因；err 非 *Error 时返回 ok=false。
func ReasonOf(err error) (Reason, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason, true
	}
	return 0, false
}

const (
	maxNow        = int64(1_000_000_000_000_000) // 10^15
	maxGeneration = int64(1_000_000)             // 10^6
	maxCooldownK  = 3                            // 冷却等待倍数上限 min(k,3)
)

// bucketState 是桶的三态。
type bucketState int

const (
	stateFree bucketState = iota
	stateOccupied
	stateCooling
)

// bucket 记录单个桶（下标 H 到 B-1 有效）的状态。
type bucket struct {
	state bucketState
	owner string // 占用时为所属实验；冷却时为上一任所有者 o
	r     int64  // 冷却释放时刻
	k     int    // 累计冷却次数（重新分配时保留，Reshuffle 清零）
}

// allocation 是一个实验占用的连续桶区间 [start, start+count)。
type allocation struct {
	start int
	count int
}

// Orchestrator 是互斥实验层流量编排器，所有方法可并发调用。
type Orchestrator struct {
	mu sync.RWMutex

	B  int   // 总桶数
	H  int   // 保留桶数（0 到 H-1 为对照组）
	Cd int64 // 冷却期
	P  int64 // 盐步长

	g      int64 // 世代
	maxNow int64 // 已被接受操作的最大 now

	buckets []bucket
	exps    map[string]allocation

	lastScan int // 非导出计数器：最近一次选段扫描考察的桶数，供测试验证
}

// New 构造编排器。buckets 为桶数 B（2 到 10^6），reserved 为保留桶数 H
// （0 到 B-1），cooldown 为冷却期 Cd（0 到 10^9），saltStep 为盐步长 P
// （1 到 10^6）。
func New(buckets, reserved int, cooldown, saltStep int64) (*Orchestrator, error) {
	if buckets < 2 || buckets > 1_000_000 {
		return nil, &Error{Reason: ReasonInvalidParams, Op: "New", Detail: "B out of range"}
	}
	if reserved < 0 || reserved > buckets-1 {
		return nil, &Error{Reason: ReasonInvalidParams, Op: "New", Detail: "H out of range"}
	}
	if cooldown < 0 || cooldown > 1_000_000_000 {
		return nil, &Error{Reason: ReasonInvalidParams, Op: "New", Detail: "Cd out of range"}
	}
	if saltStep < 1 || saltStep > 1_000_000 {
		return nil, &Error{Reason: ReasonInvalidParams, Op: "New", Detail: "P out of range"}
	}
	return &Orchestrator{
		B:       buckets,
		H:       reserved,
		Cd:      cooldown,
		P:       saltStep,
		buckets: make([]bucket, buckets),
		exps:    make(map[string]allocation),
	}, nil
}

// validNow 校验 now 是否在 0 到 10^15。
func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// availableFor 报告桶 i 在时刻 now 对实验 id 是否可用。
func (o *Orchestrator) availableFor(i int, id string, now int64) bool {
	b := &o.buckets[i]
	switch b.state {
	case stateFree:
		return true
	case stateCooling:
		if b.owner == id {
			return true // 对上一任所有者始终可用
		}
		return now >= b.r+o.Cd*int64(min(b.k, maxCooldownK))
	}
	return false
}

// Claim 为实验 id 分配 n 个连续可用桶，返回区间起点。
// 取起点最小的、整段 n 个桶对 id 都可用的位置；单遍扫描，考察桶数不超过 B。
func (o *Orchestrator) Claim(id string, n int, now int64) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.checkClaim(id, n, now); err != nil {
		return 0, err
	}
	o.lastScan = 0
	run := 0
	start := -1
	for i := o.H; i < o.B; i++ {
		o.lastScan++
		if o.availableFor(i, id, now) {
			if run == 0 {
				start = i
			}
			run++
			if run == n {
				o.assignLocked(id, start, n)
				o.maxNow = now
				return start, nil
			}
		} else {
			run = 0
			start = -1
		}
	}
	return 0, &Error{Reason: ReasonInsufficientCapacity, Op: "Claim", Detail: "no analysis"}
}

// checkClaim 按拒绝优先级校验 Claim 的前置条件。
func (o *Orchestrator) checkClaim(id string, n int, now int64) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateN(n, o); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if _, ok := o.exps[id]; ok {
		return &Error{Reason: ReasonExperimentExists, Op: "Claim", Detail: "id=" + id}
	}
	if err := o.checkClock(now); err != nil {
		return err
	}
	return nil
}

func validateID(id string) error {
	if id == "" {
		return &Error{Reason: ReasonInvalidParams, Op: "validate", Detail: "empty id"}
	}
	return nil
}

func validateN(n int, o *Orchestrator) error {
	if n < 1 || n > o.B-o.H {
		return &Error{Reason: ReasonInvalidParams, Op: "validate", Detail: "n out of range"}
	}
	return nil
}

func validateNow(now int64) error {
	if !validNow(now) {
		return &Error{Reason: ReasonInvalidParams, Op: "validate", Detail: "now out of range"}
	}
	return nil
}

func (o *Orchestrator) checkClock(now int64) error {
	if now < o.maxNow {
		return &Error{Reason: ReasonClockRollback, Op: "clock", Detail: "now rollback"}
	}
	return nil
}

// assignLocked 把 [start, start+n) 分配给 id（占用态），各桶 k 保留。
func (o *Orchestrator) assignLocked(id string, start, n int) {
	for i := start; i < start+n; i++ {
		o.buckets[i].state = stateOccupied
		o.buckets[i].owner = id
	}
	o.exps[id] = allocation{start: start, count: n}
}

// Resize 把实验 id 的桶数调整为 n2：缩小时保留前 n2 个桶，其余进入冷却；
// 扩大时只能向右原地延伸，紧邻右侧的桶须全部对 id 可用，不得搬迁。
func (o *Orchestrator) Resize(id string, n2 int, now int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.checkResize(id, n2, now); err != nil {
		return err
	}
	// 校验通过后执行调整。
	return o.applyResize(id, n2, now)
}

// checkResize 校验 Resize 的前置条件。
func (o *Orchestrator) checkResize(id string, n2 int, now int64) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateN(n2, o); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if _, ok := o.exps[id]; !ok {
		return &Error{Reason: ReasonExperimentNotExists, Op: "Resize", Detail: "id=" + id}
	}
	if err := o.checkClock(now); err != nil {
		return err
	}
	return nil
}

// applyResize 执行 Resize 的具体调整。
func (o *Orchestrator) applyResize(id string, n2 int, now int64) error {
	o.lastScan = 0
	a := o.exps[id]
	switch {
	case n2 == a.count:
		// 空操作，但仍推进时钟。
	case n2 < a.count:
		// 缩小：保留起点起前 n2 个桶，其余进入冷却。
		for i := a.start + n2; i < a.start+a.count; i++ {
			o.setCooling(i, id, now)
		}
	case n2 > a.count:
		// 扩大：只能向右原地延伸，紧邻右侧的桶须全部对 id 可用。
		need := n2 - a.count
		// 先检查是否越过 B-1。
		if a.start+a.count+need > o.B {
			return &Error{Reason: ReasonInsufficientCapacity, Op: "Resize", Detail: "overflow"}
		}
		// 单遍扫描确认可用性。
		for i := a.start + a.count; i < a.start+a.count+need; i++ {
			o.lastScan++
			if !o.availableFor(i, id, now) {
				return &Error{Reason: ReasonInsufficientCapacity, Op: "Resize", Detail: "blocked"}
			}
		}
		// 执行扩张。
		for i := a.start + a.count; i < a.start+a.count+need; i++ {
			o.buckets[i].state = stateOccupied
			o.buckets[i].owner = id
		}
	}
	// 更新实验记录。
	a.count = n2
	o.exps[id] = a
	o.maxNow = now
	return nil
}

// setCooling 把桶 i 设为冷却态，记录上一任所有者与释放时刻，k 加一。
func (o *Orchestrator) setCooling(i int, id string, now int64) {
	o.buckets[i].state = stateCooling
	o.buckets[i].owner = id
	o.buckets[i].r = now
	o.buckets[i].k++
}

// Release 释放实验 id 的全部桶进入冷却。
func (o *Orchestrator) Release(id string, now int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.checkRelease(id, now); err != nil {
		return err
	}
	return o.applyRelease(id, now)
}

// checkRelease 校验 Release 的前置条件。
func (o *Orchestrator) checkRelease(id string, now int64) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if _, ok := o.exps[id]; !ok {
		return &Error{Reason: ReasonExperimentNotExists, Op: "Release", Detail: "id=" + id}
	}
	if err := o.checkClock(now); err != nil {
		return err
	}
	return nil
}

// applyRelease 执行 Release。
func (o *Orchestrator) applyRelease(id string, now int64) error {
	a := o.exps[id]
	for i := a.start; i < a.start+a.count; i++ {
		o.setCooling(i, id, now)
	}
	delete(o.exps, id)
	o.maxNow = now
	return nil
}

// Reshuffle 进行世代重盐。
func (o *Orchestrator) Reshuffle(now int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.checkReshuffle(now); err != nil {
		return err
	}
	return o.applyReshuffle(now)
}

// checkReshuffle 校验 Reshuffle 的前置条件。
func (o *Orchestrator) checkReshuffle(now int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if err := o.checkClock(now); err != nil {
		return err
	}
	if o.g >= maxGeneration {
		return &Error{Reason: ReasonGenerationExhausted, Op: "Reshuffle", Detail: "g exhausted"}
	}
	return nil
}

// applyReshuffle 执行 Reshuffle。
func (o *Orchestrator) applyReshuffle(now int64) error {
	o.g++
	for i := o.H; i < o.B; i++ {
		if o.buckets[i].state == stateCooling {
			o.buckets[i].state = stateFree
			o.buckets[i].owner = ""
			o.buckets[i].r = 0
		}
		o.buckets[i].k = 0
	}
	o.maxNow = now
	return nil
}

// LookupKind 是 Lookup 的结果类别。
type LookupKind int

const (
	// LookupControl 桶号小于 H，返回对照组。
	LookupControl LookupKind = iota
	// LookupFree 桶空闲，无实验。
	LookupFree
	// LookupCooling 桶冷却中，无实验，附带原所有者与对他人可用时刻。
	LookupCooling
	// LookupOccupied 桶被占用，返回所属实验。
	LookupOccupied
)

// LookupResult 是 Lookup 的返回结果。
type LookupResult struct {
	Kind        LookupKind
	Bucket      int    // 映射到的桶号
	Experiment  string // 占用时所属实验 id
	PrevOwner   string // 冷却时上一任所有者
	AvailableAt int64  // 冷却时对其他实验的可用时刻 r+Cd*min(k,3)
}

// Lookup 把用户哈希 h（0 到 2^62-1）映射到所属实验。
func (o *Orchestrator) Lookup(h int64) LookupResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.lookupLocked(h)
}

func (o *Orchestrator) lookupLocked(h int64) LookupResult {
	// 映射公式：(h + g*P) mod B。
	b := int((h + o.g*o.P) % int64(o.B))
	res := LookupResult{Bucket: b}
	if b < o.H {
		res.Kind = LookupControl
		return res
	}
	bk := &o.buckets[b]
	switch bk.state {
	case stateFree:
		res.Kind = LookupFree
	case stateCooling:
		res.Kind = LookupCooling
		res.PrevOwner = bk.owner
		res.AvailableAt = bk.r + o.Cd*int64(min(bk.k, maxCooldownK))
	case stateOccupied:
		res.Kind = LookupOccupied
		res.Experiment = bk.owner
	}
	return res
}

// Usage 返回空闲、占用、冷却三类桶的个数。
type Usage struct {
	Free     int
	Occupied int
	Cooling  int
}

// Usage 统计三类桶的个数（冷却含已过期但未被重新分配者）。
func (o *Orchestrator) Usage() Usage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	var u Usage
	for i := o.H; i < o.B; i++ {
		switch o.buckets[i].state {
		case stateFree:
			u.Free++
		case stateOccupied:
			u.Occupied++
		case stateCooling:
			u.Cooling++
		}
	}
	return u
}
