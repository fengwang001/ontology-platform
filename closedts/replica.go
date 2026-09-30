package closedts

import "sync"

// Replica 从副本：接收乱序/重复投递的日志与发布，按日志序号顺序应用，
// 并在闭合时间戳覆盖且已追平时就地服务历史读。
type Replica struct {
	mu         sync.Mutex
	id         int
	appliedSeq uint64              // 已连续应用到的日志序号
	pending    map[uint64]LogEntry // 乱序到达、等待前序日志的缓冲
	publishes  []Publish           // 已收到的发布（可乱序、可重复）
	data       map[string][]versionedValue
}

func newReplica(id int) *Replica {
	return &Replica{
		id:      id,
		pending: make(map[uint64]LogEntry),
		data:    make(map[string][]versionedValue),
	}
}

// Receive 处理一条经注入网络送达的消息，重复投递安全。
func (r *Replica) Receive(msg Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch m := msg.(type) {
	case LogEntry:
		if m.Seq <= r.appliedSeq {
			return // 重复投递
		}
		if _, dup := r.pending[m.Seq]; dup {
			return // 重复投递
		}
		r.pending[m.Seq] = m
		for {
			next, ok := r.pending[r.appliedSeq+1]
			if !ok {
				break
			}
			delete(r.pending, next.Seq)
			r.appliedSeq = next.Seq
			r.data[next.Key] = append(r.data[next.Key], versionedValue{ts: next.TS, seq: next.Seq, value: next.Value})
		}
	case Publish:
		for _, p := range r.publishes {
			if p == m {
				return // 重复投递
			}
		}
		r.publishes = append(r.publishes, m)
	}
}

// AppliedSeq 返回已连续应用到的日志序号。
func (r *Replica) AppliedSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appliedSeq
}

// Read 在读时间戳 t 上就地服务历史读。
//
// 可服务条件：存在某条已收到的发布 p，满足 t <= p.Closed 且 r.appliedSeq >= p.MaxSeq。
// 能服务时必须服务；不能服务时，若没有任何已收到发布的闭合时间戳不小于 t，
// 返回 ErrNotClosed（未闭合），否则返回 ErrNotCaughtUp（未追上）。
func (r *Replica) Read(key string, t int64) (string, bool, error) {
	if t < 0 {
		return "", false, ErrNegativeTimestamp
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	closedSeen := false
	for _, p := range r.publishes {
		if p.Closed < t {
			continue
		}
		closedSeen = true
		if r.appliedSeq >= p.MaxSeq {
			v, ok := readAt(r.data, key, t)
			return v, ok, nil
		}
	}
	if !closedSeen {
		return "", false, ErrNotClosed
	}
	return "", false, ErrNotCaughtUp
}
