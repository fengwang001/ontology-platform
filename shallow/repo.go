package shallow

import (
	"context"
	"sort"
	"sync"
)

// Repo 是一个浅克隆本地仓库；所有方法可安全并发调用。
type Repo struct {
	mu     sync.RWMutex
	remote Remote
	state  *repoState
	reach  *reachSet // 延迟失效的可达性缓存，仅在 mu 下访问
	opSeq  uint64    // 仅被实际改变状态的成功操作推进
}

// Load 校验并载入本地仓库；状态非法时返回 ErrIllegalState。
func Load(remote Remote, s Snapshot) (*Repo, error) {
	st := newState()
	for _, cm := range s.Commits {
		st.commits[cm.ID] = cm
	}
	for _, b := range s.Blobs {
		st.blobs[b.ID] = b
	}
	for name, tip := range s.Refs {
		if name == "" {
			return nil, ErrInvalidArg
		}
		st.refs[name] = tip
	}
	for _, id := range s.Boundary {
		st.boundary[id] = struct{}{}
	}
	for _, cm := range st.commits {
		for _, b := range cm.Blobs {
			st.blobRef[b]++
		}
	}

	// 引用必须指向本地提交。
	for _, tip := range st.refs {
		if _, ok := st.commits[tip]; !ok {
			return nil, ErrIllegalState
		}
	}
	// 边界提交必须在本地存在。
	for id := range st.boundary {
		if _, ok := st.commits[id]; !ok {
			return nil, ErrIllegalState
		}
	}
	// 边界之外的本地提交，全部父必须在本地；碰巧持有的边界父被视为不存在。
	for id, cm := range st.commits {
		if _, isBoundary := st.boundary[id]; isBoundary {
			continue
		}
		for _, p := range cm.Parents {
			if _, ok := st.commits[p]; !ok {
				return nil, ErrIllegalState
			}
		}
	}
	return &Repo{remote: remote, state: st}, nil
}

// OpSeq 返回当前操作序号：仅实际改变状态的成功操作会推进它。
func (r *Repo) OpSeq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.opSeq
}

// SetRef 新建或更新引用；引用不得指向不在本地的提交。
func (r *Repo) SetRef(name string, target CommitID) (uint64, error) {
	if name == "" {
		return 0, ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.state.commits[target]; !ok {
		return 0, ErrIllegalState
	}
	changed := r.state.refs[name] != target
	r.state.refs[name] = target
	if changed {
		r.reach = nil
		r.opSeq++
	}
	return r.opSeq, nil
}

// DeleteRef 删除引用；引用不存在返回 ErrRefNotFound。
func (r *Repo) DeleteRef(name string) error {
	if name == "" {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.state.refs[name]; !ok {
		return ErrRefNotFound
	}
	delete(r.state.refs, name)
	r.reach = nil
	r.opSeq++
	return nil
}

// GetRef 读取引用指向与当前序号。
func (r *Repo) GetRef(name string) (CommitID, uint64, error) {
	if name == "" {
		return "", 0, ErrInvalidArg
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tip, ok := r.state.refs[name]
	if !ok {
		return "", 0, ErrRefNotFound
	}
	return tip, r.opSeq, nil
}

// Boundary 返回浅边界集合的稳定快照（排序，便于测试断言）。
func (r *Repo) Boundary() []CommitID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]CommitID, 0, len(r.state.boundary))
	for id := range r.state.boundary {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// HasCommit 回答提交对象是否在本地（无论可达性）。
func (r *Repo) HasCommit(id CommitID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.state.commits[id]
	return ok
}

// HasBlob 回答内容对象是否在本地（无论可达性）。
func (r *Repo) HasBlob(id BlobID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.state.blobs[id]
	return ok
}

// IsCommitReachable 的开销为 O(1) 哈希查询，不随本地对象总数增长
// （首次或失效后仅按可达子图大小计算一次并缓存）。
func (r *Repo) IsCommitReachable(id CommitID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rs := r.reachableLocked()
	_, ok := rs.commits[id]
	return ok
}

// IsBlobReachable 判断内容对象是否被至少一个可达提交直接引用。
func (r *Repo) IsBlobReachable(id BlobID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rs := r.reachableLocked()
	_, ok := rs.blobs[id]
	return ok
}

// GC 删除所有不可达的本地提交与内容对象。
// 浅边界内提交与引用指向的提交恒可达，因而受到保护；
// 删除不可达提交后，仅剩不可达提交引用的内容对象也一并删除。
// 回收与所有写操作在同一把互斥锁上线性化，因此不可能删除并发深化
// 刚拉取到的对象：深化要么整体先于 GC 提交，要么整体后于 GC 重拉。
func (r *Repo) GC() (GCResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rs := computeReachable(r.state)
	// 可达性回答严格按题意（遇边界停止）；但回收后本地状态仍须合法：
	// 边界之外的本地提交全部父必须在本地。因此回收保留集为
	// 「可达提交 ∪ 可达提交到浅边界之间、被其依赖的祖先闭包」：
	// 从保留节点沿父展开时，边界提交保留但其父视为不存在（不展开）。
	keepCommit := map[CommitID]struct{}{}
	var queue []CommitID
	for id := range rs.commits {
		keepCommit[id] = struct{}{}
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, isBoundary := r.state.boundary[id]; isBoundary {
			continue
		}
		cm := r.state.commits[id]
		for _, p := range cm.Parents {
			if _, ok := r.state.commits[p]; !ok {
				continue
			}
			if _, kept := keepCommit[p]; kept {
				continue
			}
			keepCommit[p] = struct{}{}
			queue = append(queue, p)
		}
	}

	res := GCResult{}
	for id, cm := range r.state.commits {
		if _, ok := keepCommit[id]; ok {
			continue
		}
		delete(r.state.commits, id)
		delete(r.state.boundary, id)
		res.Objects++
		for _, b := range cm.Blobs {
			r.state.blobRef[b]--
			if r.state.blobRef[b] <= 0 {
				delete(r.state.blobRef, b)
			}
		}
	}
	// 内容对象只按可达性回收：被任一可达提交引用则保留，其余删除。
	// 被保留但本身不可达的祖先提交引用的 blob 随 blobRef 保留计数，
	// 但仍因不可达而删除，计数同步清掉，避免悬空计数。
	for id, b := range r.state.blobs {
		if _, reachableBlob := rs.blobs[id]; reachableBlob {
			continue
		}
		delete(r.state.blobs, id)
		delete(r.state.blobRef, id)
		res.Objects++
		res.Bytes += b.Size
	}
	if res.Objects > 0 {
		r.opSeq++
	}
	r.reach = rs
	return res, nil
}

// reachableLocked 返回（必要时惰性计算）当前状态的可达集合。
func (r *Repo) reachableLocked() *reachSet {
	if r.reach == nil {
		r.reach = computeReachable(r.state)
	}
	return r.reach
}

var _ = context.Background

// debugSnapshot 仅供测试：导出当前完整本地状态。
type debugSnapshot struct {
	Commits  map[CommitID]Commit
	Blobs    map[BlobID]Blob
	Refs     map[string]CommitID
	Boundary map[CommitID]struct{}
	ReachC   map[CommitID]struct{}
	ReachB   map[BlobID]struct{}
}

func (r *Repo) debugSnapshot() debugSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rs := r.reachableLocked()
	s := debugSnapshot{
		Commits:  map[CommitID]Commit{},
		Blobs:    map[BlobID]Blob{},
		Refs:     map[string]CommitID{},
		Boundary: map[CommitID]struct{}{},
		ReachC:   rs.commits,
		ReachB:   rs.blobs,
	}
	for id, c := range r.state.commits {
		s.Commits[id] = c
	}
	for id, b := range r.state.blobs {
		s.Blobs[id] = b
	}
	for n, id := range r.state.refs {
		s.Refs[n] = id
	}
	for id := range r.state.boundary {
		s.Boundary[id] = struct{}{}
	}
	return s
}
