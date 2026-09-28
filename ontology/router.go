// Package ontology 提供以会话为单位的读写一致性令牌路由组件。
//
// 主库每次写入产生严格递增的序号；只读副本各自维护已应用序号。
// 每个会话持有一个一致性令牌，写入后令牌至少前进到该写入的序号，
// 读取只会被路由到已应用序号不低于令牌的副本，从而保证同一会话
// 能够读到自己的写入，且观察到的进度单调不降。
package ontology

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// RejectReason 以可区分的枚举值标识请求被拒绝的原因。
type RejectReason string

const (
	// ReasonUnknownSession 表示请求引用了尚未注册的会话。
	ReasonUnknownSession RejectReason = "unknown_session"
	// ReasonUnknownReplica 表示请求引用了尚未注册的副本。
	ReasonUnknownReplica RejectReason = "unknown_replica"
	// ReasonReplicaLagging 表示没有任何副本的已应用序号达到会话令牌要求。
	ReasonReplicaLagging RejectReason = "replica_lagging"
	// ReasonAdvanceRollback 表示推进请求的目标序号小于副本当前进度。
	ReasonAdvanceRollback RejectReason = "advance_rollback"
	// ReasonAdvanceAhead 表示推进请求的目标序号超过主库当前序号。
	ReasonAdvanceAhead RejectReason = "advance_ahead"
	// ReasonDuplicateSession 表示注册了已存在的会话。
	ReasonDuplicateSession RejectReason = "duplicate_session"
	// ReasonDuplicateReplica 表示注册了已存在的副本。
	ReasonDuplicateReplica RejectReason = "duplicate_replica"
)

// RejectError 描述一次被拒绝的操作及其可区分原因。
type RejectError struct {
	Reason RejectReason
	msg    string
}

func (e *RejectError) Error() string { return e.msg }

// Router 是会话一致性令牌路由器。
//
// 单个互斥锁保护全部内部状态；所有判定（拒绝或成功）都在持锁期间
// 一次性完成并记录日志，被拒绝的操作不会对任何状态产生改动。
type Router struct {
	mu       sync.Mutex
	logw     io.Writer
	primary  uint64
	replicas map[string]uint64
	sessions map[string]uint64
}

// 日志中各字段使用固定的 key=value 形式，便于排查与断言。
// 日志在互斥锁内写入，保证并发下行序不交错。

// NewRouter 创建路由器，logw 指定判定日志的输出目的地，为 nil 时丢弃日志。
func NewRouter(logw io.Writer) *Router {
	return &Router{
		logw:     logw,
		replicas: make(map[string]uint64),
		sessions: make(map[string]uint64),
	}
}

// AddSession 注册一个令牌初始为 0 的新会话。
func (r *Router) AddSession(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; ok {
		r.logf("op=add_session session=%q rejected reason=%s basis=session_already_registered", id, ReasonDuplicateSession)
		return reject(ReasonDuplicateSession, fmt.Sprintf("session %q already registered", id))
	}
	r.sessions[id] = 0
	r.logf("op=add_session session=%q accepted reason=none basis=new_session_token=0", id)
	return nil
}

// AddReplica 注册一个已应用序号初始为 0 的新副本。
func (r *Router) AddReplica(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.replicas[name]; ok {
		r.logf("op=add_replica replica=%q rejected reason=%s basis=replica_already_registered", name, ReasonDuplicateReplica)
		return reject(ReasonDuplicateReplica, fmt.Sprintf("replica %q already registered", name))
	}
	r.replicas[name] = 0
	r.logf("op=add_replica replica=%q accepted reason=none basis=new_replica_applied=0", name)
	return nil
}

// WriteResult 是一次写入的结果。
type WriteResult struct {
	Sequence uint64
	Token    uint64
}

// Write 代表会话 id 在主库执行一次写入：主库序号加一，
// 会话令牌前进到自身与新序号的较大值。
func (r *Router) Write(id string) (WriteResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[id]
	if !ok {
		r.logf("op=write session=%q rejected reason=%s basis=session_not_registered", id, ReasonUnknownSession)
		return WriteResult{}, reject(ReasonUnknownSession, fmt.Sprintf("unknown session %q", id))
	}
	r.primary++
	newSeq := r.primary
	if token < newSeq {
		token = newSeq
	}
	r.sessions[id] = token
	r.logf("op=write session=%q accepted reason=none basis=primary_increment sequence=%d token=%d", id, newSeq, token)
	return WriteResult{Sequence: newSeq, Token: token}, nil
}

// ReadResult 是一次读取的结果。
type ReadResult struct {
	Replica  string
	Observed uint64
	Token    uint64
}

// Read 代表会话 id 的一次只读请求：在已应用序号不低于令牌的副本中
// 选择进度最小者（名字字典序打破并列）；没有合格副本时立即返回
// ReasonReplicaLagging 错误。读后令牌取自身与观察进度的较大值。
func (r *Router) Read(id string) (ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[id]
	if !ok {
		r.logf("op=read session=%q rejected reason=%s basis=session_not_registered", id, ReasonUnknownSession)
		return ReadResult{}, reject(ReasonUnknownSession, fmt.Sprintf("unknown session %q", id))
	}

	// 先收集合格副本，再按“进度最小、名字字典序”排序选择，
	// 使选择结果只取决于状态而与 map 遍历顺序无关（确定性）。
	type candidate struct {
		name    string
		applied uint64
	}
	var eligible []candidate
	for name, applied := range r.replicas {
		if applied >= token {
			eligible = append(eligible, candidate{name, applied})
		}
	}
	if len(eligible) == 0 {
		r.logf("op=read session=%q rejected reason=%s basis=no_replica_applied_ge_token token=%d", id, ReasonReplicaLagging, token)
		return ReadResult{}, reject(ReasonReplicaLagging, fmt.Sprintf("no replica has applied sequence >= session token %d", token))
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].applied != eligible[j].applied {
			return eligible[i].applied < eligible[j].applied
		}
		return eligible[i].name < eligible[j].name
	})
	chosen := eligible[0]
	if token < chosen.applied {
		token = chosen.applied
	}
	r.sessions[id] = token
	r.logf("op=read session=%q accepted reason=none basis=min_applied_ge_token replica=%q observed=%d candidates=%d token=%d",
		id, chosen.name, chosen.applied, len(eligible), token)
	return ReadResult{Replica: chosen.name, Observed: chosen.applied, Token: token}, nil
}

// Advance 通知名为 name 的副本把已应用序号推进到 applied。
// applied 不得小于当前进度（回退），也不得超过主库序号（超前）。
func (r *Router) Advance(name string, applied uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.replicas[name]
	if !ok {
		r.logf("op=advance replica=%q applied=%d rejected reason=%s basis=replica_not_registered", name, applied, ReasonUnknownReplica)
		return reject(ReasonUnknownReplica, fmt.Sprintf("unknown replica %q", name))
	}
	if applied < current {
		r.logf("op=advance replica=%q applied=%d rejected reason=%s basis=target_below_current current=%d", name, applied, ReasonAdvanceRollback, current)
		return reject(ReasonAdvanceRollback, fmt.Sprintf("advance for replica %q from %d to %d is a rollback", name, current, applied))
	}
	if applied > r.primary {
		r.logf("op=advance replica=%q applied=%d rejected reason=%s basis=target_above_primary primary=%d", name, applied, ReasonAdvanceAhead, r.primary)
		return reject(ReasonAdvanceAhead, fmt.Sprintf("advance for replica %q to %d exceeds primary sequence %d", name, applied, r.primary))
	}
	r.replicas[name] = applied
	r.logf("op=advance replica=%q accepted reason=none basis=target_within_bounds applied=%d previous=%d", name, applied, current)
	return nil
}

// Token 返回会话当前令牌（供测试与诊断使用）。
func (r *Router) Token(id string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[id]
	if !ok {
		return 0, reject(ReasonUnknownSession, fmt.Sprintf("unknown session %q", id))
	}
	return token, nil
}

// Applied 返回副本当前已应用序号（供测试与诊断使用）。
func (r *Router) Applied(name string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	applied, ok := r.replicas[name]
	if !ok {
		return 0, reject(ReasonUnknownReplica, fmt.Sprintf("unknown replica %q", name))
	}
	return applied, nil
}

// Primary 返回主库当前序号（供测试与诊断使用）。
func (r *Router) Primary() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.primary
}

// reject 构造带原因的拒绝错误。
func reject(reason RejectReason, msg string) *RejectError {
	return &RejectError{Reason: reason, msg: msg}
}

func (r *Router) logf(format string, args ...any) {
	if r.logw == nil {
		return
	}
	fmt.Fprintf(r.logw, format+"\n", args...)
}
