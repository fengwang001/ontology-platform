package sessionroute

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// WriteResult 是一次写入的结果。
type WriteResult struct {
	// Sequence 是本次写入在主库上产生的递增序号。
	Sequence int64
	// Token 是写入完成后会话持有的新令牌。
	Token int64
}

// ReadResult 是一次只读请求路由的结果。
type ReadResult struct {
	// Replica 是被选中副本的名字。
	Replica string
	// Observed 是该副本当前已应用的序号（读观察进度）。
	Observed int64
	// Token 是读完成后会话持有的新令牌。
	Token int64
}

// Router 以会话为单位把读写请求路由到只读副本。
//
// 零值不可用，必须通过 NewRouter 构造。所有方法可被并发调用。
type Router struct {
	mu       sync.Mutex
	master   int64
	replicas map[string]int64
	sessions map[string]int64
	log      io.Writer
}

// NewRouter 创建路由器：replicaNames 为全部只读副本（初始进度均为 0）。
//
// 副本名字必须非空且不得重复。log 用于打印输入、所选副本与判定依据，
// 传 nil 表示关闭日志。
func NewRouter(replicaNames []string, log io.Writer) (*Router, error) {
	replicas := make(map[string]int64, len(replicaNames))
	for _, name := range replicaNames {
		if name == "" {
			return nil, fmt.Errorf("sessionroute: replica name must not be empty")
		}
		if _, dup := replicas[name]; dup {
			return nil, fmt.Errorf("sessionroute: duplicate replica name %q", name)
		}
		replicas[name] = 0
	}
	return &Router{
		replicas: replicas,
		sessions: make(map[string]int64),
		log:      log,
	}, nil
}

// RegisterSession 注册一个新会话，令牌初始为 0。重名返回错误。
func (r *Router) RegisterSession(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return fmt.Errorf("sessionroute: session name must not be empty")
	}
	if _, exists := r.sessions[name]; exists {
		return fmt.Errorf("sessionroute: duplicate session name %q", name)
	}
	r.sessions[name] = 0
	r.tracef("register session=%s token=0", name)
	return nil
}

// Write 在主库上产生一个递增序号，并把会话令牌提升到该序号。
func (r *Router) Write(session string) (WriteResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[session]
	if !ok {
		r.tracef("write session=%s reject=%s (会话未注册，主库序号保持 %d)",
			session, ReasonUnknownSession, r.master)
		return WriteResult{}, reject(ReasonUnknownSession,
			fmt.Sprintf("sessionroute: unknown session %q", session))
	}
	r.master++
	seq := r.master
	if seq > token {
		token = seq
	}
	r.sessions[session] = token
	r.tracef("write session=%s seq=%d token=%d (主库序号 %d -> %d，令牌取 max)",
		session, seq, token, seq-1, seq)
	return WriteResult{Sequence: seq, Token: token}, nil
}

// Read 把会话的读请求路由到已应用序号不低于其令牌、且进度最小的副本。
func (r *Router) Read(session string) (ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.sessions[session]
	if !ok {
		r.tracef("read session=%s reject=%s (会话未注册，不改变任何状态)",
			session, ReasonUnknownSession)
		return ReadResult{}, reject(ReasonUnknownSession,
			fmt.Sprintf("sessionroute: unknown session %q", session))
	}
	chosen := ""
	var chosenProgress int64
	candidates := make([]string, 0)
	for name, progress := range r.replicas {
		if progress >= token {
			candidates = append(candidates, name)
			if chosen == "" || progress < chosenProgress ||
				(progress == chosenProgress && name < chosen) {
				chosen = name
				chosenProgress = progress
			}
		}
	}
	if chosen == "" {
		r.tracef("read session=%s token=%d reject=%s (所有副本进度均低于令牌)",
			session, token, ReasonReplicaBehind)
		return ReadResult{}, reject(ReasonReplicaBehind,
			fmt.Sprintf("sessionroute: no replica caught up to token %d for session %q",
				token, session))
	}
	sort.Strings(candidates)
	if chosenProgress > token {
		token = chosenProgress
	}
	r.sessions[session] = token
	r.tracef("read session=%s token=%d replica=%s observed=%d candidates=%v "+
		"(候选均 >= 令牌，选进度最小者，名字字典序打破并列；令牌取 max)",
		session, token, chosen, chosenProgress, candidates)
	return ReadResult{Replica: chosen, Observed: chosenProgress, Token: token}, nil
}

// Advance 把指定副本的已应用序号推进到 progress。
func (r *Router) Advance(replica string, progress int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.replicas[replica]
	if !ok {
		r.tracef("advance replica=%s progress=%d reject=%s (副本不存在，不改变任何状态)",
			replica, progress, ReasonUnknownReplica)
		return reject(ReasonUnknownReplica,
			fmt.Sprintf("sessionroute: unknown replica %q", replica))
	}
	if progress < current {
		r.tracef("advance replica=%s progress=%d reject=%s (当前 %d，不允许回退)",
			replica, progress, ReasonProgressRollback, current)
		return reject(ReasonProgressRollback,
			fmt.Sprintf("sessionroute: replica %q progress %d is behind current %d",
				replica, progress, current))
	}
	if progress > r.master {
		r.tracef("advance replica=%s progress=%d reject=%s (主库序号仅 %d，不允许超前)",
			replica, progress, ReasonProgressAhead, r.master)
		return reject(ReasonProgressAhead,
			fmt.Sprintf("sessionroute: replica %q progress %d exceeds master %d",
				replica, progress, r.master))
	}
	if progress == current {
		r.tracef("advance replica=%s progress=%d unchanged (幂等推进，进度保持 %d)",
			replica, progress, current)
		return nil
	}
	r.replicas[replica] = progress
	r.tracef("advance replica=%s progress=%d (副本进度 %d -> %d)",
		replica, progress, current, progress)
	return nil
}

func (r *Router) tracef(format string, args ...any) {
	if r.log != nil {
		fmt.Fprintf(r.log, "[sessionroute] "+format+"\n", args...)
	}
}
