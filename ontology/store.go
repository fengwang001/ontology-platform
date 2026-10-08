package ontology

import "sync"

// WriteRequest 描述一次对单个对象实例的写入请求。
// 同一逻辑请求的所有重试必须复用同一 ID 并原样携带 Ticket。
type WriteRequest struct {
	// ID 是该逻辑请求的唯一标识。
	ID RequestID
	// ObjectID 是目标对象实例标识。
	ObjectID string
	// BaseVersion 是调用方读取数据时的版本号，用于乐观并发检查。
	BaseVersion uint64
	// Patch 是要应用的属性补丁（属性级最后写入获胜语义）。
	Patch map[string]any
	// MaxRetries 是允许的最大重试次数（不含首次尝试）。
	MaxRetries uint64
	// Ticket 是跨重试携带的优先级依据，由仲裁器签发与更新。
	Ticket Ticket

	// OnConflict 可选：当尝试以 OutcomeConflict 结束时回调，
	// 调用方可以此基于最新快照重算 Patch 与 BaseVersion
	// （用于读-改-写场景）。为 nil 时系统自动刷新 BaseVersion。
	OnConflict func(version uint64, props map[string]any, req *WriteRequest)
}

// AttemptRecord 记录一次尝试的完整信息，用于审计与重放核验。
type AttemptRecord struct {
	// Outcome 是本次尝试的互斥结果。
	Outcome Outcome
	// Ticket 是本次尝试裁定后的权威票据（到达时刻与失败次数）。
	Ticket Ticket
	// Score 是本次尝试裁定时该请求的优先级分数。
	Score uint64
	// Version 是提交后的版本号（仅 OutcomeCommitted 时有效）。
	Version uint64
	// DecisionSeq 是本次尝试对应裁定决策的逻辑时钟值；
	// 对于消费既有通知的尝试，指向产生该通知的轮次。
	DecisionSeq uint64
}

// WriteResult 是一次写入的最终结果。
type WriteResult struct {
	// Outcome 是最终结果，只可能是 OutcomeCommitted 或 OutcomeExhausted。
	Outcome Outcome
	// Version 是提交后的版本号（仅 OutcomeCommitted 时有效）。
	Version uint64
	// Attempts 按顺序记录每一次尝试，包含裁定依据与结果。
	Attempts []AttemptRecord
}

// Store 是本体对象存储，按实例进行公平的并发写入仲裁。
type Store struct {
	mu        sync.Mutex
	instances map[string]*instance
}

// NewStore 创建一个空的对象存储。
func NewStore() *Store {
	return &Store{instances: make(map[string]*instance)}
}

// instance 返回指定对象实例的仲裁器（不存在则创建）。
func (s *Store) instance(objectID string) *instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[objectID]
	if !ok {
		inst = newInstance()
		s.instances[objectID] = inst
	}
	return inst
}

// Write 执行一次写入：驱动尝试-重试循环直到提交或重试耗尽。
// 由于每次尝试都保证全局有进展（恰有一个请求提交或请求被计入失败），
// 且任一请求的失败次数存在上界，该循环必然终止。
func (s *Store) Write(req *WriteRequest) WriteResult {
	inst := s.instance(req.ObjectID)
	res := WriteResult{}
	for {
		r := inst.attempt(req)
		res.Attempts = append(res.Attempts, AttemptRecord{
			Outcome:     r.Outcome,
			Ticket:      r.Ticket,
			Score:       r.Score,
			Version:     r.Version,
			DecisionSeq: r.DecisionSeq,
		})
		switch r.Outcome {
		case OutcomeCommitted:
			res.Outcome = OutcomeCommitted
			res.Version = r.Version
			return res
		case OutcomeExhausted:
			res.Outcome = OutcomeExhausted
			return res
		case OutcomeConflict:
			// 写冲突：允许调用方基于最新快照重算补丁。
			snap := inst.snapshot()
			if req.OnConflict != nil {
				req.OnConflict(snap.Version, snap.Props, req)
			} else {
				req.BaseVersion = snap.Version
			}
		case OutcomePreempted:
			// 被挤出：携带已提升的票据，阻塞等待实例状态变化后再重试，
			// 避免空转；每次被唤醒都意味着有竞争者提交或退出。
			inst.waitForChange(r.Generation)
		}
	}
}

// Snapshot 返回实例对外可观察状态（版本号、属性、最后提交时间）的快照。
func (s *Store) Snapshot(objectID string) (uint64, map[string]any) {
	inst := s.instance(objectID)
	snap := inst.snapshot()
	return snap.Version, snap.Props
}

// Decisions 返回实例完整裁定决策日志的副本，可用于重放核验。
func (s *Store) Decisions(objectID string) []Decision {
	inst := s.instance(objectID)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	out := make([]Decision, len(inst.log))
	copy(out, inst.log)
	return out
}

// snapshot 是实例级快照获取。
func (inst *instance) snapshot() instanceState {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.snapshotLocked()
}
