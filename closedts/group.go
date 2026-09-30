package closedts

import "sync"

// Group 一个副本组：内含主副本状态与若干从副本句柄。
// 写、发布、读均可被并发调用。
type Group struct {
	mu       sync.Mutex
	clock    Clock
	net      Network
	lag      int64
	replicas map[int]*Replica

	nextSeq  uint64           // 下一个待分配的日志序号，从 1 开始
	inflight map[uint64]int64 // 在途写：序号 -> 最终时间戳
	closed   int64            // 已发布的最大闭合时间戳
	data     map[string][]versionedValue
}

// NewGroup 创建副本组。lag 为目标滞后，followerIDs 为从副本标识。
// lag 为负时整体拒绝并返回 ErrNegativeLag。
func NewGroup(clock Clock, net Network, lag int64, followerIDs ...int) (*Group, error) {
	if lag < 0 {
		return nil, ErrNegativeLag
	}
	g := &Group{
		clock:    clock,
		net:      net,
		lag:      lag,
		replicas: make(map[int]*Replica, len(followerIDs)),
		nextSeq:  1,
		inflight: make(map[uint64]int64),
		data:     make(map[string][]versionedValue),
	}
	for _, id := range followerIDs {
		g.replicas[id] = newReplica(id)
	}
	return g, nil
}

// Write 一次已提案、可能尚未应用的写。
type Write struct {
	group   *Group
	seq     uint64
	ts      int64
	key     string
	value   string
	applied bool
}

// Timestamp 返回写的最终时间戳（抬高后）。
func (w *Write) Timestamp() int64 { return w.ts }

// Seq 返回写分配到的日志序号。
func (w *Write) Seq() uint64 { return w.seq }

// BeginWrite 提案一次写：校验、按需要抬高时间戳、分配连续日志序号并进入在途集合。
// ts 为负时整体拒绝并返回 ErrNegativeTimestamp，不改变任何状态。
func (g *Group) BeginWrite(key, value string, ts int64) (*Write, error) {
	if ts < 0 {
		return nil, ErrNegativeTimestamp
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if ts <= g.closed {
		ts = g.closed + 1
	}
	w := &Write{group: g, seq: g.nextSeq, ts: ts, key: key, value: value}
	g.nextSeq++
	g.inflight[w.seq] = ts
	return w, nil
}

// Apply 应用写：写入主副本状态、离开在途集合，并经注入网络广播日志。
func (w *Write) Apply() {
	g := w.group
	g.mu.Lock()
	if w.applied {
		g.mu.Unlock()
		return
	}
	w.applied = true
	delete(g.inflight, w.seq)
	g.data[w.key] = append(g.data[w.key], versionedValue{ts: w.ts, seq: w.seq, value: w.value})
	replicas := make([]int, 0, len(g.replicas))
	for id := range g.replicas {
		replicas = append(replicas, id)
	}
	g.mu.Unlock()

	entry := LogEntry{Seq: w.seq, TS: w.ts, Key: w.key, Value: w.value}
	for _, id := range replicas {
		g.net.Send(id, entry)
	}
}

// Publish 计算并发布闭合时间戳，返回实际发布内容。
func (g *Group) Publish() Publish {
	g.mu.Lock()
	candidate := g.clock.Now() - g.lag
	for _, ts := range g.inflight {
		if candidate >= ts {
			candidate = ts - 1
		}
	}
	if candidate > g.closed {
		g.closed = candidate
	}
	p := Publish{Closed: g.closed, MaxSeq: g.nextSeq - 1}
	replicas := make([]int, 0, len(g.replicas))
	for id := range g.replicas {
		replicas = append(replicas, id)
	}
	g.mu.Unlock()

	for _, id := range replicas {
		g.net.Send(id, p)
	}
	return p
}

// Closed 返回当前已发布的最大闭合时间戳。
func (g *Group) Closed() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// ReadPrimary 在主副本上按时间戳 t 读（全部已应用写）。
// ts 为负时返回 ErrNegativeTimestamp。
func (g *Group) ReadPrimary(key string, ts int64) (string, bool, error) {
	if ts < 0 {
		return "", false, ErrNegativeTimestamp
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := readAt(g.data, key, ts)
	return v, ok, nil
}

// Read 在指定从副本上按时间戳 t 读。
// 副本不存在返回 ErrReplicaNotFound，ts 为负返回 ErrNegativeTimestamp；
// 两种情况下均不改变任何副本状态。
func (g *Group) Read(replicaID int, key string, ts int64) (string, bool, error) {
	if ts < 0 {
		return "", false, ErrNegativeTimestamp
	}
	g.mu.Lock()
	r, ok := g.replicas[replicaID]
	g.mu.Unlock()
	if !ok {
		return "", false, ErrReplicaNotFound
	}
	return r.Read(key, ts)
}

// Replica 返回指定从副本句柄，不存在时返回 nil。
func (g *Group) Replica(replicaID int) *Replica {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.replicas[replicaID]
}
