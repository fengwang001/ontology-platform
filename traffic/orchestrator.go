package traffic

import (
	"errors"
	"sync"
)

// 桶状态（保留桶不参与三态机）。
const (
	StateFree     = 0 // 空闲：从未分配，或已被 Reshuffle 清空
	StateHeld     = 1 // 占用：属于某实验
	StateCooldown = 2 // 冷却：记录上一任所有者与释放时刻
)

// 参数与取值边界。
const (
	MaxGeneration = 1_000_000
	MaxNow        = int64(10_000_000_000_000_000) // 1e15
	MaxHash       = int64(1<<62 - 1)
	MaxCooldown   = int64(1_000_000_000) // 1e9
	MaxSaltStep   = int64(1_000_000)
	MaxBuckets    = 1_000_000
)

// 可区分的拒绝原因（按优先级返回第一个）。
var (
	ErrInvalidArgument     = errors.New("traffic: invalid argument")
	ErrExperimentExists    = errors.New("traffic: experiment already exists")
	ErrExperimentNotFound  = errors.New("traffic: experiment not found")
	ErrClockRewound        = errors.New("traffic: clock rewound")
	ErrGenerationExhausted = errors.New("traffic: generation exhausted")
	ErrCapacity            = errors.New("traffic: insufficient capacity")
)

// Bucket 是对外快照的单桶完整状态。
type Bucket struct {
	State    int
	Owner    string // 占用者；冷却时为上一任所有者 o
	Released int64  // 冷却时释放时刻 r
	K        int    // 冷却次数；重新分配时保留
}

// LookupResult 是 Lookup 的判定结果。
type LookupResult struct {
	Bucket      int64
	Control     bool   // 桶号落在保留对照组 [0,H)
	Found       bool   // 命中占用桶
	Experiment  string // Found 时所属实验 id
	Cooldown    bool   // 命中冷却桶
	PrevOwner   string // Cooldown 时上一任所有者 o
	AvailableAt int64  // Cooldown 时对他人可用时刻 r+Cd*min(k,3)
}

// Usage 是三态桶计数（保留桶不计入）。
type Usage struct {
	Free     int
	Held     int
	Cooldown int
}

// bucket 是内部桶记录。
type bucket struct {
	state int
	owner string
	rel   int64
	k     int
}

// expRange 记录实验占用的连续半开区间 [Start, End)。
type expRange struct {
	start int
	end   int
}

// Orchestrator 是互斥实验层流量编排器；所有方法可并发调用。
type Orchestrator struct {
	mu       sync.RWMutex
	buckets  []bucket // 长度恒为 B；[0,H) 为保留桶，状态固定为 StateFree 且永不扫描
	exps     map[string]expRange
	bucketsN int
	hold     int
	cd       int64
	p        int64
	g        int64
	maxNow   int64
	freeN    int
	heldN    int
	coolN    int

	// 非导出计数器：最近一次 Claim/Resize 选段扫描考察过的桶数。
	lastScanExamined int
}

// New 构造编排器；参数不合法返回 ErrInvalidArgument。
func New(buckets int, hold int, cooldown int64, saltStep int64) (*Orchestrator, error) {
	if buckets < 2 || buckets > MaxBuckets ||
		hold < 0 || hold > buckets-1 ||
		cooldown < 0 || cooldown > MaxCooldown ||
		saltStep < 1 || saltStep > MaxSaltStep {
		return nil, ErrInvalidArgument
	}
	o := &Orchestrator{
		buckets:  make([]bucket, buckets),
		exps:     make(map[string]expRange),
		bucketsN: buckets,
		hold:     hold,
		cd:       cooldown,
		p:        saltStep,
		freeN:    buckets - hold,
	}
	return o, nil
}

// Claim 为新实验分配起点最小的整段 n 桶；返回起点（区间 [start,start+n)）。
func (o *Orchestrator) Claim(id string, n int, now int64) (start int, err error) {
	if id == "" || n < 1 || n > o.bucketsN-o.hold || now < 0 || now > MaxNow {
		return 0, ErrInvalidArgument
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.exps[id]; ok {
		return 0, ErrExperimentExists
	}
	if now < o.maxNow {
		return 0, ErrClockRewound
	}

	o.lastScanExamined = 0
	runStart := -1
	run := 0
	found := -1
	for i := o.hold; i < o.bucketsN; i++ {
		o.lastScanExamined++
		if o.availableTo(&o.buckets[i], id, now) {
			if run == 0 {
				runStart = i
			}
			run++
			if run >= n {
				found = runStart
				break
			}
		} else {
			runStart = -1
			run = 0
		}
	}
	if found < 0 {
		return 0, ErrCapacity
	}

	for i := found; i < found+n; i++ {
		b := &o.buckets[i]
		switch b.state {
		case StateFree:
			o.freeN--
		case StateCooldown:
			o.coolN--
		}
		b.state = StateHeld
		b.owner = id
	}
	o.heldN += n
	o.exps[id] = expRange{start: found, end: found + n}
	o.maxNow = now
	return found, nil
}

// Resize 将实验占用数调整为 n2；缩容保头、扩容仅向右原地延伸。
func (o *Orchestrator) Resize(id string, n2 int, now int64) (start int, err error) {
	if id == "" || n2 < 1 || n2 > o.bucketsN-o.hold || now < 0 || now > MaxNow {
		return 0, ErrInvalidArgument
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rng, ok := o.exps[id]
	if !ok {
		return 0, ErrExperimentNotFound
	}
	if now < o.maxNow {
		return 0, ErrClockRewound
	}

	count := rng.end - rng.start
	switch {
	case n2 == count:
		o.maxNow = now
		return rng.start, nil
	case n2 < count:
		for i := rng.start + n2; i < rng.end; i++ {
			b := &o.buckets[i]
			b.state = StateCooldown
			b.owner = id
			b.rel = now
			b.k++
		}
		released := count - n2
		o.heldN -= released
		o.coolN += released
		o.exps[id] = expRange{start: rng.start, end: rng.start + n2}
		o.maxNow = now
		return rng.start, nil
	default:
		need := n2 - count
		o.lastScanExamined = 0
		for i := rng.end; i < rng.end+need; i++ {
			o.lastScanExamined++
			if i >= o.bucketsN || !o.availableTo(&o.buckets[i], id, now) {
				return 0, ErrCapacity
			}
		}
		for i := rng.end; i < rng.end+need; i++ {
			b := &o.buckets[i]
			if b.state == StateFree {
				o.freeN--
			} else {
				o.coolN--
			}
			b.state = StateHeld
			b.owner = id
		}
		o.heldN += need
		o.exps[id] = expRange{start: rng.start, end: rng.end + need}
		o.maxNow = now
		return rng.start, nil
	}
}

// Release 释放实验全部桶进入冷却。
func (o *Orchestrator) Release(id string, now int64) error {
	if id == "" || now < 0 || now > MaxNow {
		return ErrInvalidArgument
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rng, ok := o.exps[id]
	if !ok {
		return ErrExperimentNotFound
	}
	if now < o.maxNow {
		return ErrClockRewound
	}
	for i := rng.start; i < rng.end; i++ {
		b := &o.buckets[i]
		b.state = StateCooldown
		b.owner = id
		b.rel = now
		b.k++
	}
	n := rng.end - rng.start
	o.heldN -= n
	o.coolN += n
	delete(o.exps, id)
	o.maxNow = now
	return nil
}

// Reshuffle 世代加一，清空全部冷却桶并把所有桶 k 清零。
func (o *Orchestrator) Reshuffle(now int64) (generation int64, err error) {
	if now < 0 || now > MaxNow {
		return 0, ErrInvalidArgument
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if now < o.maxNow {
		return 0, ErrClockRewound
	}
	if o.g >= MaxGeneration {
		return o.g, ErrGenerationExhausted
	}
	for i := o.hold; i < o.bucketsN; i++ {
		b := &o.buckets[i]
		if b.state == StateCooldown {
			b.state = StateFree
			b.owner = ""
			b.rel = 0
		}
		b.k = 0
	}
	o.freeN += o.coolN
	o.coolN = 0
	o.g++
	o.maxNow = now
	return o.g, nil
}

// Lookup 把用户哈希映射到桶并判定归属；不带 now，不做时钟检查。
func (o *Orchestrator) Lookup(h int64) (LookupResult, error) {
	if h < 0 || h > MaxHash {
		return LookupResult{}, ErrInvalidArgument
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	idx := (h + o.g*o.p) % int64(o.bucketsN)
	res := LookupResult{Bucket: idx}
	if idx < int64(o.hold) {
		res.Control = true
		return res, nil
	}
	b := &o.buckets[idx]
	switch b.state {
	case StateHeld:
		res.Found = true
		res.Experiment = b.owner
	case StateCooldown:
		res.Cooldown = true
		res.PrevOwner = b.owner
		res.AvailableAt = b.rel + o.cd*int64(cooldownMult(b.k))
	}
	return res, nil
}

// Usage 返回三态桶计数。
func (o *Orchestrator) Usage() Usage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return Usage{Free: o.freeN, Held: o.heldN, Cooldown: o.coolN}
}

// Generation 返回当前世代 g。
func (o *Orchestrator) Generation() int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.g
}

// MaxNow 返回已被接受操作的最大 now。
func (o *Orchestrator) MaxNow() int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.maxNow
}

// availableTo 判断桶在 now 时刻对实验 id 是否可用于分配：
// 空闲恒可用；冷却桶对上一任所有者恒可用，对他人需 now >= r+Cd*min(k,3)。
// 占用桶不属于任何可分配连续段。
func (o *Orchestrator) availableTo(b *bucket, id string, now int64) bool {
	switch b.state {
	case StateFree:
		return true
	case StateCooldown:
		if b.owner == id {
			return true
		}
		return now >= b.rel+o.cd*int64(cooldownMult(b.k))
	default:
		return false
	}
}

// cooldownMult 返回第 k 次冷却的等待倍数：1,2,3,3,...（min(k,3)）。
func cooldownMult(k int) int {
	if k > 3 {
		return 3
	}
	if k < 1 {
		return 1
	}
	return k
}
