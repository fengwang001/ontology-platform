package srv

import (
	"sort"
	"sync"
)

// 合法取值范围常量。
const (
	MaxPort     = 65535
	MaxPriority = 65535
	MaxWeight   = 65535
	MaxTTL      = 1_000_000_000
	MaxCooldown = 1_000_000_000
	MaxWr       = 1_000_000_000
	MaxCoolCap  = 1_000_000_000
	MaxNow      = 1_000_000_000_000_000
)

// record 是一条 SRV 记录的内部状态。
type record struct {
	target        string
	port          int
	priority      int
	weight        int
	expiresAt     int64 // 到期时刻：登记/更新时的 now+ttl
	seq           uint64
	cooldownUntil int64 // 冷却截止时刻，只增不减
	failures      uint64
	lastFail      int64
	hasLastFail   bool
}

// Selector 是带过期淘汰与指数失败冷却的 DNS SRV 加权选择器。
// 所有方法可并发调用，结果等价于某个串行顺序。
type Selector struct {
	mu      sync.Mutex
	cap     int
	coolCap int64
	wr      int64
	records map[key]*record
	nextSeq uint64
}

type key struct {
	target string
	port   int
}

// New 构造选择器。Cap 至少为 1，coolCap 与 wr 须在 [1, 1e9]，否则整体拒绝。
func New(capacity int, coolCap, wr int64) (*Selector, error) {
	if capacity < 1 {
		return nil, newError(ErrInvalidParam, "cap %d 小于 1", capacity)
	}
	if coolCap < 1 || coolCap > MaxCoolCap {
		return nil, newError(ErrInvalidParam, "coolCap %d 不在 [1, 1e9]", coolCap)
	}
	if wr < 1 || wr > MaxWr {
		return nil, newError(ErrInvalidParam, "wr %d 不在 [1, 1e9]", wr)
	}
	return &Selector{
		cap:     capacity,
		coolCap: coolCap,
		wr:      wr,
		records: make(map[key]*record),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

func available(rec *record, now int64) bool {
	return now < rec.expiresAt && now >= rec.cooldownUntil
}

// Add 登记或更新一条记录。
// 重复登记同一 (target, port) 视为更新：覆盖 priority、weight，
// 到期时刻改为 now+ttl，保留登记序号、冷却截止时刻、f 与 lf。
// 拒绝原因按顺序只报第一个：参数非法、时间非法、已满。
func (s *Selector) Add(target string, port, priority, weight int, ttl, now int64) error {
	if target == "" {
		return newError(ErrInvalidParam, "add: target 为空")
	}
	if port < 1 || port > MaxPort {
		return newError(ErrInvalidParam, "add: port %d 不在 [1, 65535]", port)
	}
	if priority < 0 || priority > MaxPriority {
		return newError(ErrInvalidParam, "add: priority %d 不在 [0, 65535]", priority)
	}
	if weight < 0 || weight > MaxWeight {
		return newError(ErrInvalidParam, "add: weight %d 不在 [0, 65535]", weight)
	}
	if ttl < 1 || ttl > MaxTTL {
		return newError(ErrInvalidParam, "add: ttl %d 不在 [1, 1e9]", ttl)
	}
	if !validNow(now) {
		return newError(ErrInvalidTime, "add: now %d 不在 [0, 1e15]", now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key{target, port}
	if rec, ok := s.records[k]; ok {
		rec.priority = priority
		rec.weight = weight
		rec.expiresAt = now + ttl
		return nil
	}
	if len(s.records) >= s.cap {
		s.purgeLocked(now)
		if len(s.records) >= s.cap {
			return newError(ErrFull, "add: 容量 %d 已满", s.cap)
		}
	}
	s.nextSeq++
	s.records[k] = &record{
		target:    target,
		port:      port,
		priority:  priority,
		weight:    weight,
		expiresAt: now + ttl,
		seq:       s.nextSeq,
	}
	return nil
}

// Failure 上报一次失败。若 lf 为空或 now >= lf+Wr 则先把 f 清零；
// 然后 f 加一、lf 置为 now，有效冷却为 min(CoolCap, cooldown*2^min(f-1,30))，
// 冷却截止时刻取 max(原值, now+有效冷却)。
// 拒绝原因按顺序：参数非法、时间非法、记录不存在、记录已到期。
func (s *Selector) Failure(target string, port int, cooldown, now int64) error {
	if target == "" {
		return newError(ErrInvalidParam, "failure: target 为空")
	}
	if cooldown < 1 || cooldown > MaxCooldown {
		return newError(ErrInvalidParam, "failure: cooldown %d 不在 [1, 1e9]", cooldown)
	}
	if !validNow(now) {
		return newError(ErrInvalidTime, "failure: now %d 不在 [0, 1e15]", now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[key{target, port}]
	if !ok {
		return newError(ErrNotFound, "failure: 记录 (%s, %d) 不存在", target, port)
	}
	if now >= rec.expiresAt {
		return newError(ErrExpired, "failure: 记录 (%s, %d) 已到期", target, port)
	}

	if !rec.hasLastFail || now >= rec.lastFail+s.wr {
		rec.failures = 0
	}
	rec.failures++
	rec.lastFail = now
	rec.hasLastFail = true

	shift := rec.failures - 1
	if shift > 30 {
		shift = 30
	}
	effective := cooldown << shift
	if effective > s.coolCap {
		effective = s.coolCap
	}
	if until := now + effective; until > rec.cooldownUntil {
		rec.cooldownUntil = until
	}
	return nil
}

// Success 上报一次成功，只把 f 清零，不改冷却截止时刻与 lf。
// 拒绝原因按顺序：参数非法、时间非法、记录不存在、记录已到期。
func (s *Selector) Success(target string, port int, now int64) error {
	if target == "" {
		return newError(ErrInvalidParam, "success: target 为空")
	}
	if !validNow(now) {
		return newError(ErrInvalidTime, "success: now %d 不在 [0, 1e15]", now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[key{target, port}]
	if !ok {
		return newError(ErrNotFound, "success: 记录 (%s, %d) 不存在", target, port)
	}
	if now >= rec.expiresAt {
		return newError(ErrExpired, "success: 记录 (%s, %d) 已到期", target, port)
	}
	rec.failures = 0
	return nil
}

// Pick 按 RFC 2782 规则选择一条可用记录，不修改任何状态。
// 先取存在可用记录的最小 priority 组；组内 weight 为 0 的记录按登记序号
// 升序排在最前，再接 weight 大于 0 的按登记序号升序；令 r1 = r mod (S+1)，
// 按序累计权重，选第一个累计值不小于 r1 的记录。
func (s *Selector) Pick(r uint64, now int64) (string, int, error) {
	if !validNow(now) {
		return "", 0, newError(ErrInvalidTime, "pick: now %d 不在 [0, 1e15]", now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	minPriority := -1
	for _, rec := range s.records {
		if available(rec, now) && (minPriority < 0 || rec.priority < minPriority) {
			minPriority = rec.priority
		}
	}
	if minPriority < 0 {
		return "", 0, newError(ErrNoAvailable, "pick: 没有可用记录")
	}

	group := make([]*record, 0)
	for _, rec := range s.records {
		if available(rec, now) && rec.priority == minPriority {
			group = append(group, rec)
		}
	}
	sort.Slice(group, func(i, j int) bool {
		zi, zj := group[i].weight == 0, group[j].weight == 0
		if zi != zj {
			return zi
		}
		return group[i].seq < group[j].seq
	})

	var sum uint64
	for _, rec := range group {
		sum += uint64(rec.weight)
	}
	r1 := r % (sum + 1)
	var cumulative uint64
	for _, rec := range group {
		cumulative += uint64(rec.weight)
		if cumulative >= r1 {
			return rec.target, rec.port, nil
		}
	}
	// 不可达：r1 <= sum 保证必有累计值不小于 r1 的记录。
	return "", 0, newError(ErrNoAvailable, "pick: 没有可用记录")
}

// Purge 删除到期时刻不大于 now 的记录，返回删除个数。
func (s *Selector) Purge(now int64) (int, error) {
	if !validNow(now) {
		return 0, newError(ErrInvalidTime, "purge: now %d 不在 [0, 1e15]", now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.purgeLocked(now), nil
}

func (s *Selector) purgeLocked(now int64) int {
	removed := 0
	for k, rec := range s.records {
		if rec.expiresAt <= now {
			delete(s.records, k)
			removed++
		}
	}
	return removed
}
