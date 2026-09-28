package replication

import "sync"

// Replica 表示一个副本：一段日志 + 世代起始位点缓存。
type Replica struct {
	mu      sync.RWMutex
	id      string
	entries []Entry
	cache   []GenerationStart
}

func NewReplica(id string) *Replica {
	return &Replica{id: id}
}

func (r *Replica) ID() string { return r.id }

// EndOffset 返回日志结束位点（即下一条待写入的位点）。
func (r *Replica) EndOffset() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return uint64(len(r.entries))
}

// Entries 返回日志快照，供并发只读查询。
func (r *Replica) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Entry(nil), r.entries...)
}

// Cache 返回世代缓存快照，供并发只读查询。
func (r *Replica) Cache() []GenerationStart {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]GenerationStart(nil), r.cache...)
}

// becomeLeaderLocked 在当选领导者时记录新世代的起始位点。
func (r *Replica) becomeLeaderLocked(gen uint64) error {
	if gen == 0 {
		return ErrInvalidArgument
	}
	if latest, ok := latestGeneration(r.cache); ok && gen <= latest {
		return ErrInvalidArgument
	}
	r.cache = append(r.cache, GenerationStart{Generation: gen, StartOffset: uint64(len(r.entries))})
	return nil
}

// BecomeLeader 记录一次领导者当选：以当前日志末端作为新世代起始位点。
func (r *Replica) BecomeLeader(gen uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.becomeLeaderLocked(gen)
}

// appendLocked 追加日志；遇到新世代时同步追加缓存项。
// 不变式：日志非空时缓存首项起始位点必为 0，缓存按世代严格递增。
func (r *Replica) appendLocked(entries []Entry) error {
	for _, e := range entries {
		if e.Generation == 0 {
			return ErrInvalidArgument
		}
		latest, ok := latestGeneration(r.cache)
		switch {
		case !ok:
			r.cache = append(r.cache, GenerationStart{Generation: e.Generation, StartOffset: 0})
		case e.Generation > latest:
			r.cache = append(r.cache, GenerationStart{Generation: e.Generation, StartOffset: uint64(len(r.entries))})
		case e.Generation < latest:
			return ErrInvalidArgument
		}
		r.entries = append(r.entries, e)
	}
	return nil
}

// Append 由领导者本地写入日志。
func (r *Replica) Append(entries ...Entry) error {
	if len(entries) == 0 {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendLocked(entries)
}

// truncateLocked 把日志截断到 offset，并删除起始位点不小于 offset 的缓存项。
func (r *Replica) truncateLocked(offset uint64) {
	r.entries = r.entries[:offset]
	keep := 0
	for _, gs := range r.cache {
		if gs.StartOffset < offset {
			r.cache[keep] = gs
			keep++
		}
	}
	r.cache = r.cache[:keep]
}

// generationBoundsLocked 返回世代 gen 在本副本日志中的 [start, end) 区间。
func (r *Replica) generationBoundsLocked(gen uint64) (start, end uint64, ok bool) {
	for i, gs := range r.cache {
		if gs.Generation == gen {
			start = gs.StartOffset
			if i+1 < len(r.cache) {
				end = r.cache[i+1].StartOffset
			} else {
				end = uint64(len(r.entries))
			}
			return start, end, true
		}
	}
	return 0, 0, false
}

func latestGeneration(cache []GenerationStart) (uint64, bool) {
	if len(cache) == 0 {
		return 0, false
	}
	return cache[len(cache)-1].Generation, true
}
