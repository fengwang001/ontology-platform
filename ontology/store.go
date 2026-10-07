package ontology

import "sync"

type rec struct {
	version   int64
	state     string
	props     Props
	links     map[string][]string
	token     int64
	holder    string
	expiresAt int64
}

// Store 是带乐观版本号与独占占用权的实例存储。
type Store struct {
	mu        sync.Mutex
	clock     Clock
	log       *DecisionLog
	instances map[string]*rec
	linkTypes map[string]LinkType
	seq       int64
}

// NewStore 创建存储。clock 为 nil 时使用墙上时间。
func NewStore(clock Clock) *Store {
	if clock == nil {
		clock = WallClock{}
	}
	return &Store{
		clock:     clock,
		log:       NewDecisionLog(),
		instances: make(map[string]*rec),
		linkTypes: make(map[string]LinkType),
	}
}

// AddLinkType 登记链接类型及其基数约束。
func (s *Store) AddLinkType(lt LinkType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linkTypes[lt.Name] = lt
}

// CreateInstance 以初始状态创建实例，初始版本为 1。
func (s *Store) CreateInstance(key, state string, props Props) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[key] = &rec{version: 1, state: state, props: cloneProps(props), links: make(map[string][]string)}
	s.recordLocked(Decision{Kind: "create", Caller: "system", Keys: []string{key},
		State: state, Patch: cloneProps(props),
		Outcome: OutcomeCommitted, NewVersion: 1})
}

// Snapshot 返回某实例当前完整状态的深拷贝。
func (s *Store) Snapshot(key string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.instances[key]
	if !ok {
		return Instance{}, false
	}
	now := s.clock.Now()
	s.evictExpiredLocked(r, now)
	return Instance{
		Key:        key,
		Version:    r.version,
		State:      r.state,
		Props:      cloneProps(r.props),
		Links:      cloneLinks(r.links),
		Occupied:   r.holder != "",
		Holder:     r.holder,
		LeaseUntil: r.expiresAt,
	}, true
}

// Log 返回判定日志。
func (s *Store) Log() *DecisionLog { return s.log }

// TryAcquire 立即申请一组实例的独占占用权；任一实例不可得即整体拒绝，
// 绝不部分持有、绝不阻塞排队。keys 会按全局规范序处理。
func (s *Store) TryAcquire(actionID string, keys []string, ttlMS int64) (*Lease, Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	ordered := normalizeKeys(keys)
	if len(ordered) == 0 || actionID == "" || ttlMS <= 0 {
		s.recordLocked(Decision{Kind: "acquire", At: now, Caller: actionID, Keys: ordered,
			TTL: ttlMS, Outcome: OutcomeInvalidLease, Reason: "invalid arguments"})
		return nil, OutcomeInvalidLease
	}

	// 第一遍：全部键必须存在，并惰性裁定已到期的占用。
	for _, key := range ordered {
		r, ok := s.instances[key]
		if !ok {
			s.recordLocked(Decision{Kind: "acquire", At: now, Caller: actionID, Keys: ordered,
				TTL: ttlMS, Outcome: OutcomeVersionStale, Reason: "instance not found",
				ConflictKey: key})
			return nil, OutcomeVersionStale
		}
		s.evictExpiredLocked(r, now)
	}

	// 第二遍（全局规范序）：任一存活占用即整体拒绝。所有申请都按同一规范序、
	// 在同一把互斥锁内原子判定，不可能出现两方各持一部分再相互等待的循环；
	// 谁被拒绝完全由判定时刻（日志序号）确定：后申请者让先持有者，
	// 结果可重放、非随机。
	for _, key := range ordered {
		r := s.instances[key]
		if r.holder != "" {
			out := OutcomeOccupied
			reason := "single instance already held"
			if len(ordered) > 1 {
				out = OutcomeLockConflict
				reason = "cross-instance acquire conflict"
			}
			if r.holder == actionID {
				reason = "caller already holds a lease on the instance"
			}
			s.recordLocked(Decision{Kind: "acquire", At: now, Caller: actionID, Keys: ordered,
				TTL: ttlMS, Outcome: out, Reason: reason, Holder: r.holder, ConflictKey: key})
			return nil, out
		}
	}

	lease := &Lease{
		store:     s,
		actionID:  actionID,
		keys:      ordered,
		ttl:       ttlMS,
		expiresAt: now + ttlMS,
		staged:    make(map[string]stagedState, len(ordered)),
	}
	for _, key := range ordered {
		r := s.instances[key]
		r.token++
		r.holder = actionID
		r.expiresAt = lease.expiresAt
		lease.token = r.token
		lease.staged[key] = stagedState{
			state: r.state,
			props: cloneProps(r.props),
			links: cloneLinks(r.links),
		}
	}
	s.recordLocked(Decision{Kind: "acquire", At: now, Caller: actionID, Keys: ordered,
		TTL: ttlMS, Outcome: OutcomeCommitted, Token: lease.token})
	return lease, OutcomeCommitted
}

// Update 提交一次普通乐观更新。判定顺序：
// 占用冲突（可区分的“实例被占用”）→ 版本落后。
func (s *Store) Update(caller string, key string, baseVersion int64, p Patch) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	r, ok := s.instances[key]
	d := Decision{Kind: "update", At: now, Caller: caller, Keys: []string{key},
		BaseVer: baseVersion, Patch: cloneProps(Props(p))}
	if !ok {
		d.Outcome, d.Reason = OutcomeVersionStale, "instance not found"
		s.recordLocked(d)
		return OutcomeVersionStale
	}
	s.evictExpiredLocked(r, now)
	if r.holder != "" {
		// 占用期间：立即拒绝，不排队，且版本号不变。
		d.Outcome, d.Reason, d.Holder = OutcomeOptimisticRejected, "instance is exclusively occupied", r.holder
		s.recordLocked(d)
		return OutcomeOptimisticRejected
	}
	if r.version != baseVersion {
		d.Outcome, d.Reason = OutcomeVersionStale, "base version is stale"
		s.recordLocked(d)
		return OutcomeVersionStale
	}
	if r.props == nil {
		r.props = Props{}
	}
	for k, v := range p {
		r.props[k] = v
	}
	r.version++
	d.Outcome, d.NewVersion = OutcomeCommitted, r.version
	s.recordLocked(d)
	return OutcomeCommitted
}

// Link 以乐观方式尝试通过 linkType 建立 source→target 链接。判定顺序与
// Update 相同：占用 → 版本 → 基数。
func (s *Store) Link(caller, source string, baseVersion int64, linkType, target string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	d := Decision{Kind: "link", At: now, Caller: caller, Keys: []string{source, target}, BaseVer: baseVersion}
	r, ok := s.instances[source]
	if !ok || s.instances[target] == nil {
		d.Outcome, d.Reason, d.ConflictKey = OutcomeVersionStale, "endpoint instance not found", target
		s.recordLocked(d)
		return OutcomeVersionStale
	}
	lt, ok := s.linkTypes[linkType]
	if !ok {
		d.Outcome, d.Reason = OutcomeCardinality, "unknown link type"
		s.recordLocked(d)
		return OutcomeCardinality
	}
	s.evictExpiredLocked(r, now)
	if r.holder != "" {
		d.Outcome, d.Reason, d.Holder = OutcomeOptimisticRejected, "instance is exclusively occupied", r.holder
		s.recordLocked(d)
		return OutcomeOptimisticRejected
	}
	if r.version != baseVersion {
		d.Outcome, d.Reason = OutcomeVersionStale, "base version is stale"
		s.recordLocked(d)
		return OutcomeVersionStale
	}
	list := r.links[linkType]
	for _, t := range list {
		if t == target {
			d.Outcome, d.Reason = OutcomeCommitted, "link already present"
			s.recordLocked(d)
			return OutcomeCommitted
		}
	}
	if lt.MaxOut > 0 && len(list) >= lt.MaxOut {
		d.Outcome, d.Reason = OutcomeCardinality, "link cardinality exceeded"
		s.recordLocked(d)
		return OutcomeCardinality
	}
	r.links[linkType] = append(list, target)
	r.version++
	d.Outcome, d.NewVersion = OutcomeCommitted, r.version
	s.recordLocked(d)
	return OutcomeCommitted
}

// aliveLocked 报告占用在 now 时刻是否仍然有效。裁定依据唯一：
// 授予时记录的 expiresAt 相对于系统唯一 Clock 的读数。
func (s *Store) aliveLocked(r *rec, now int64) bool {
	return r.holder != "" && r.expiresAt > now
}

// evictExpiredLocked 在唯一裁定依据（租约到期）满足时清理占用，并记录
// “持有方推定中止”事件，保证占用不会永久悬挂。
func (s *Store) evictExpiredLocked(r *rec, now int64) {
	if r.holder != "" && r.expiresAt <= now {
		holder := r.holder
		r.holder = ""
		r.expiresAt = 0
		s.recordLocked(Decision{Kind: "lease_expired", At: now, Caller: holder,
			Outcome: OutcomeInvalidLease,
			Reason:  "lease TTL elapsed without heartbeat; holder presumed aborted"})
	}
}

func (s *Store) recordLocked(d Decision) Decision {
	s.seq++
	d.Seq = s.seq
	if d.At == 0 {
		d.At = s.clock.Now()
	}
	return s.log.appendLocked(d)
}

func normalizeKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func cloneProps(p Props) Props {
	if p == nil {
		return Props{}
	}
	c := make(Props, len(p))
	for k, v := range p {
		c[k] = v
	}
	return c
}

func cloneLinks(m map[string][]string) map[string][]string {
	c := make(map[string][]string, len(m))
	for k, v := range m {
		c[k] = append([]string(nil), v...)
	}
	return c
}
