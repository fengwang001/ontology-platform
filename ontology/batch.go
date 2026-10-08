package ontology

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// DefaultMaxBatchSize 批量请求组大小的预设上限。
const DefaultMaxBatchSize = 1024

// BatchErrorKind 批级参数非法的类别。
type BatchErrorKind int

const (
	// BatchErrEmpty 请求组为空。
	BatchErrEmpty BatchErrorKind = iota
	// BatchErrTooLarge 请求组大小超过上限。
	BatchErrTooLarge
	// BatchErrInvalidObjectID 请求组内出现格式不合法的对象标识。
	BatchErrInvalidObjectID
)

// BatchValidationError 批级参数非法错误。整批拒绝：
// 不返回任何单项结果，也不计入任何审计记录。
type BatchValidationError struct {
	Kind BatchErrorKind
	// Index 与 ID 仅在 Kind == BatchErrInvalidObjectID 时有效，
	// 指向请求组内第一个格式不合法的对象标识。
	Index int
	ID    ObjectID
	// Limit 仅在 Kind == BatchErrTooLarge 时有效，为预设上限。
	Limit int
}

func (e *BatchValidationError) Error() string {
	switch e.Kind {
	case BatchErrEmpty:
		return "ontology: batch request group is empty"
	case BatchErrTooLarge:
		return fmt.Sprintf("ontology: batch request group exceeds limit %d", e.Limit)
	case BatchErrInvalidObjectID:
		return fmt.Sprintf("ontology: malformed object id %q at pair index %d", e.ID, e.Index)
	}
	return "ontology: invalid batch"
}

// BatchMetrics 单批内部度量，不对调用者暴露，仅用于内部验证与测试。
type BatchMetrics struct {
	// SnapshotVersion 本批锚定的状态版本，批内所有单项共用。
	SnapshotVersion uint64
	// UniqueStarts 本批实际执行搜索的去重后独立起点数量。
	UniqueStarts int
	// VisitedObjects 本批实际访问（出队扩展）的对象数。
	VisitedObjects int
	// VisitedLinks 本批实际访问（检查）的链接数。
	VisitedLinks int
}

// AuditEntry 一条批量查询审计记录。批级参数非法的批次不产生审计记录。
type AuditEntry struct {
	BatchID         uint64
	Caller          CallerID
	Items           int
	Reachable       int
	Unreachable     int
	Restricted      int
	SnapshotVersion uint64
}

// Service 批量可达性查询服务。
type Service struct {
	store    *Store
	maxBatch int

	seq     atomic.Uint64
	auditMu sync.Mutex
	audit   []AuditEntry
}

// NewService 创建服务。maxBatch <= 0 时使用 DefaultMaxBatchSize。
func NewService(store *Store, maxBatch int) *Service {
	if maxBatch <= 0 {
		maxBatch = DefaultMaxBatchSize
	}
	return &Service{store: store, maxBatch: maxBatch}
}

// validateBatch 批级参数校验，先于任何单项判定。
func validateBatch(pairs []Pair, maxBatch int) error {
	if len(pairs) == 0 {
		return &BatchValidationError{Kind: BatchErrEmpty}
	}
	if len(pairs) > maxBatch {
		return &BatchValidationError{Kind: BatchErrTooLarge, Limit: maxBatch}
	}
	for i, p := range pairs {
		if !p.Start.Valid() {
			return &BatchValidationError{Kind: BatchErrInvalidObjectID, Index: i, ID: p.Start}
		}
		if !p.End.Valid() {
			return &BatchValidationError{Kind: BatchErrInvalidObjectID, Index: i, ID: p.End}
		}
	}
	return nil
}

// BatchReachable 批量可达性查询。返回与请求组一一对应、保持原始顺序的
// 结果列表；批级参数非法时整批拒绝并返回错误。
func (s *Service) BatchReachable(pairs []Pair, caller CallerID) ([]ItemResult, error) {
	results, _, err := s.batchReachable(pairs, caller)
	return results, err
}

// Reachable 单项可达性查询，语义与 BatchReachable 的单项完全一致。
func (s *Service) Reachable(start, end ObjectID, caller CallerID) (ItemResult, error) {
	results, err := s.BatchReachable([]Pair{{Start: start, End: end}}, caller)
	if err != nil {
		return ItemResult{}, err
	}
	return results[0], nil
}

// batchReachable 内部入口：额外返回度量，供内部验证与测试使用。
func (s *Service) batchReachable(pairs []Pair, caller CallerID) ([]ItemResult, BatchMetrics, error) {
	// 批级参数校验先于任何单项判定；失败时整批拒绝，不产生审计记录。
	if err := validateBatch(pairs, s.maxBatch); err != nil {
		return nil, BatchMetrics{}, err
	}
	// 一次快照读取即线性化点：批内所有单项锚定同一逻辑时点。
	snap := s.store.snapshot()
	ev := newEvaluator(snap, caller)
	results := make([]ItemResult, len(pairs))
	for i, p := range pairs {
		results[i] = ev.evaluate(p)
	}
	metrics := BatchMetrics{
		SnapshotVersion: snap.version,
		UniqueStarts:    len(ev.reach),
		VisitedObjects:  ev.visitedObjects,
		VisitedLinks:    ev.visitedLinks,
	}
	s.recordAudit(caller, snap.version, results)
	return results, metrics, nil
}

// recordAudit 追加一条批量查询审计记录。
func (s *Service) recordAudit(caller CallerID, version uint64, results []ItemResult) {
	entry := AuditEntry{
		BatchID:         s.seq.Add(1),
		Caller:          caller,
		Items:           len(results),
		SnapshotVersion: version,
	}
	for _, r := range results {
		switch r.State {
		case StateReachable:
			entry.Reachable++
		case StateUnreachable:
			entry.Unreachable++
		case StateRestrictedUnknown:
			entry.Restricted++
		}
	}
	s.auditMu.Lock()
	s.audit = append(s.audit, entry)
	s.auditMu.Unlock()
}

// AuditLog 返回当前全部审计记录的副本。
func (s *Service) AuditLog() []AuditEntry {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	return append([]AuditEntry(nil), s.audit...)
}
