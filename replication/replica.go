package replication

import "sync"

// LogEntry 是日志中的一条消息。同一位点上 Epoch 与 ID 都相同才视为一致。
type LogEntry struct {
	Epoch  int
	ID     int64
	Leader string
}

// EpochMark 世代缓存项：世代 Epoch 从位点 Start 开始。
type EpochMark struct {
	Epoch int
	Start int
}

// Replica 单个副本的日志与世代缓存。
type Replica struct {
	mu     sync.RWMutex
	id     string
	log    []LogEntry
	epochs []EpochMark
}

// ID 返回副本名。
func (r *Replica) ID() string { return r.id }

// End 返回日志结束位点（下一条将写入的位点）。
func (r *Replica) End() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.log)
}

// EpochCache 返回世代缓存的快照副本。
func (r *Replica) EpochCache() []EpochMark {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EpochMark, len(r.epochs))
	copy(out, r.epochs)
	return out
}

// Log 返回日志的快照副本。
func (r *Replica) Log() []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LogEntry, len(r.log))
	copy(out, r.log)
	return out
}

// lastEpoch 返回当前最新世代；缓存为空时返回 -1。
func (r *Replica) lastEpoch() int {
	if len(r.epochs) == 0 {
		return -1
	}
	return r.epochs[len(r.epochs)-1].Epoch
}

// appendEntry 追加一条日志；条目世代比缓存最新世代新时同步追加缓存项。
func (r *Replica) appendEntry(e LogEntry) {
	r.log = append(r.log, e)
	if e.Epoch > r.lastEpoch() {
		r.epochs = append(r.epochs, EpochMark{Epoch: e.Epoch, Start: len(r.log) - 1})
	}
}

// elect 当选领导者：在当前结束位点登记新世代的起始缓存项。
func (r *Replica) elect(epoch int) {
	if epoch > r.lastEpoch() {
		r.epochs = append(r.epochs, EpochMark{Epoch: epoch, Start: len(r.log)})
	}
}
