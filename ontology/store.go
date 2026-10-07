package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
)

// AuditStore 是单一对象类型范围内的不可篡改审计序列。
type AuditStore struct {
	mu        sync.Mutex
	records   []*Record
	seeding   bool
	initial   map[string]string
	failNextN atomic.Int64 // 故障注入：接下来 N 次 append 失败
	// corrected[targetSeq][instance] = 该动作记录该实例的最新订正后值。
	// 订正追加时增量维护，使重放能以 O(1) 点查获得「追溯订正后的有效值」，
	// 无需为一次重放扫描全部订正记录。
	corrected map[int]map[string]string
	// rawAfter[seq][instance] = 动作记录的原始写入 After，供 O(1) 点查。
	rawAfter map[int]map[string]string
}

// NewAuditStore 创建空审计序列。
func NewAuditStore() *AuditStore {
	return &AuditStore{
		seeding:   true,
		initial:   map[string]string{},
		corrected: map[int]map[string]string{},
		rawAfter:  map[int]map[string]string{},
	}
}

// InjectAppendFailures 令接下来 n 次 append 调用失败（模拟审计持久化故障）。
// 失败发生在序号占用之前，因此不产生空洞、不产生记录。
func (s *AuditStore) InjectAppendFailures(n int) { s.failNextN.Add(int64(n)) }

// ResetFaults 清除故障注入计数（仅测试使用）。
func (s *AuditStore) ResetFaults() { s.failNextN.Store(0) }

// Seed 在第一条记录写入前登记初始实例值（模拟对象类型已有实例）。
// 一旦已有审计记录，Seed 被拒绝，保证「初始状态」不可事后伪造。
func (s *AuditStore) Seed(instance, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.seeding {
		return errors.New("seed rejected: audit sequence already started")
	}
	s.initial[instance] = value
	return nil
}

// Len 返回已持久化记录数（也是当前最大序号）。
func (s *AuditStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// Get 返回某序号记录的深拷贝（只读视图）。
func (s *AuditStore) Get(seq int) (*Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq < 1 || seq > len(s.records) {
		return nil, false
	}
	return cloneRecord(s.records[seq-1]), true
}

// viewGet 返回某序号记录的内部指针（零拷贝，仅供同模块信任的调用方）。
func (s *AuditStore) viewGet(seq int) (*Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq < 1 || seq > len(s.records) {
		return nil, false
	}
	return s.records[seq-1], true
}

// All 返回全部记录的深拷贝（只读视图），外部无法借返回值篡改内部序列。
func (s *AuditStore) All() []*Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordsCopyLocked()
}

// Range 返回开区间 (lo, hi] 内记录的深拷贝（半开切片 [lo,hi)）。
// 重放器只取所需区间，避免随历史总量线性拷贝，便于以扫描条数度量开销。
func (s *AuditStore) Range(lo, hi int) []*Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lo < 0 {
		lo = 0
	}
	if hi > len(s.records) {
		hi = len(s.records)
	}
	if lo > hi {
		return nil
	}
	out := make([]*Record, 0, hi-lo)
	for _, r := range s.records[lo:hi] {
		out = append(out, cloneRecord(r))
	}
	return out
}

// viewRange 返回区间 [lo,hi) 的「只读内部视图」（零拷贝）。
//
// 记录一旦 append 即不可变（唯一变异入口在测试白盒中），因此内部
// 重放路径可安全共享指针，避免逐条深拷贝的 GC 开销；公共 API
// （Get/All/Range）仍返回深拷贝以保证外部隔离。
func (s *AuditStore) viewRange(lo, hi int) []*Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.records)
	if lo < 0 {
		lo = 0
	}
	if hi > n {
		hi = n
	}
	if lo >= hi {
		return nil
	}
	out := make([]*Record, hi-lo)
	copy(out, s.records[lo:hi]) // 只拷贝指针切片，不深拷贝记录
	return out
}

// viewAll 返回全部记录的零拷贝只读视图（供同模块信任的重放路径）。
func (s *AuditStore) viewAll() []*Record { return s.viewRange(0, len(s.records)) }

// rawLen 返回记录数（调用方已持锁时使用）。
// initialCopy 返回初始实例状态的深拷贝。
func (s *AuditStore) initialCopy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.initial))
	for k, v := range s.initial {
		out[k] = v
	}
	return out
}

// append 在临界区内分配序号并「持久化」记录。
//
// 序号分配与写入在同一个临界区内完成：并发下要么完整成功并占用下一个
// 连续序号，要么失败且不推进序号计数器，因此不会出现重号或空洞。
func (s *AuditStore) append(kind Kind, outcome Outcome, actionID string, changes []Change, correctionOf int) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failNextN.Load() > 0 {
		s.failNextN.Add(-1)
		return nil, &AuditWriteError{msg: "injected persistence failure (no seq consumed)"}
	}

	seq := len(s.records) + 1
	prev := ""
	if len(s.records) > 0 {
		prev = s.records[len(s.records)-1].Hash
	}
	rec := &Record{
		Seq:          seq,
		ActionID:     actionID,
		Kind:         kind,
		Outcome:      outcome,
		Changes:      canonicalChanges(changes),
		CorrectionOf: correctionOf,
		PrevHash:     prev,
	}
	rec.Hash = hashRecord(rec)

	// rec 此刻尚未对外暴露，直接存入即可；返回值再做深拷贝隔离调用方。
	s.records = append(s.records, rec)
	stored := rec
	if kind == KindAction {
		raw := make(map[string]string, len(stored.Changes))
		for _, c := range stored.Changes {
			raw[c.Instance] = c.After
		}
		s.rawAfter[stored.Seq] = raw
	}
	if kind == KindCorrection {
		ov, ok := s.corrected[correctionOf]
		if !ok {
			ov = map[string]string{}
			s.corrected[correctionOf] = ov
		}
		for _, c := range stored.Changes {
			ov[c.Instance] = c.After // 追加顺序即全局串行顺序，后写为最新订正
		}
	}
	s.seeding = false
	return cloneRecord(stored), nil
}

// correctedAfter 返回某动作记录中某实例「经最新订正后」的有效值；
// 若无订正，第二个返回值为 false（调用方使用动作原始 After）。
func (s *AuditStore) correctedAfter(targetSeq int, instance string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ov, ok := s.corrected[targetSeq]; ok {
		if v, ok := ov[instance]; ok {
			return v, true
		}
	}
	return "", false
}

// effectiveValueAt 单次加锁返回某动作某实例的「原始写入值 + 最新订正值(若有)」。
// 供快照边界批量物化使用，避免逐实例重复加锁。
func (s *AuditStore) effectiveValueAt(targetSeq int, instance string) (raw, corrected string, hasCorrection bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.rawAfter[targetSeq]; ok {
		if v, ok2 := m[instance]; ok2 {
			raw = v
		}
	}
	if ov, ok := s.corrected[targetSeq]; ok {
		if v, ok2 := ov[instance]; ok2 {
			return raw, v, true
		}
	}
	return raw, "", false
}

func (s *AuditStore) recordsCopyLocked() []*Record {
	out := make([]*Record, len(s.records))
	for i, r := range s.records {
		out[i] = cloneRecord(r)
	}
	return out
}

// verifyHashChain 校验哈希链完整性，检测任何已写入记录被篡改 / 删除 / 乱序。
func (s *AuditStore) verifyHashChain() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := ""
	for i, r := range s.records {
		if r.Seq != i+1 {
			return errors.New("hash chain: sequence gap or reorder detected")
		}
		if r.PrevHash != prev {
			return errors.New("hash chain: broken prev link")
		}
		if r.Hash != hashRecord(r) {
			return errors.New("hash chain: record content tampered")
		}
		prev = r.Hash
	}
	return nil
}

func cloneRecord(r *Record) *Record {
	cp := *r
	if r.Changes != nil {
		cp.Changes = append([]Change(nil), r.Changes...)
	}
	return &cp
}
