package shallow

// repoState 是仓库的不可变逻辑状态快照：所有变更都复制后整体替换，
// 使读操作与写操作可以通过 RWMutex 获得线性一致的串行等价语义。
type repoState struct {
	commits  map[CommitID]Commit
	blobs    map[BlobID]Blob
	refs     map[string]CommitID
	boundary map[CommitID]struct{}
	blobRef  map[BlobID]int64 // 内容对象在本地被多少个本地提交引用
}

func newState() *repoState {
	return &repoState{
		commits:  map[CommitID]Commit{},
		blobs:    map[BlobID]Blob{},
		refs:     map[string]CommitID{},
		boundary: map[CommitID]struct{}{},
		blobRef:  map[BlobID]int64{},
	}
}

func (s *repoState) clone() *repoState {
	c := &repoState{
		commits:  make(map[CommitID]Commit, len(s.commits)),
		blobs:    make(map[BlobID]Blob, len(s.blobs)),
		refs:     make(map[string]CommitID, len(s.refs)),
		boundary: make(map[CommitID]struct{}, len(s.boundary)),
		blobRef:  make(map[BlobID]int64, len(s.blobRef)),
	}
	for id, cm := range s.commits {
		c.commits[id] = cm
	}
	for id, b := range s.blobs {
		c.blobs[id] = b
	}
	for name, id := range s.refs {
		c.refs[name] = id
	}
	for id := range s.boundary {
		c.boundary[id] = struct{}{}
	}
	for id, n := range s.blobRef {
		c.blobRef[id] = n
	}
	return c
}

// reachSet 是一次可达性计算的结果。
type reachSet struct {
	commits map[CommitID]struct{}
	blobs   map[BlobID]struct{}
}

func newReachSet() *reachSet {
	return &reachSet{
		commits: map[CommitID]struct{}{},
		blobs:   map[BlobID]struct{}{},
	}
}

// computeReachable 从每个引用出发沿父关系前进，遇到浅边界提交即停止
// 向上（边界提交本身可达，其父不经过）。只遍历可达子图，开销不随
// 本地不可达对象的数量增长。
func computeReachable(s *repoState) *reachSet {
	rs := newReachSet()
	var queue []CommitID
	enqueue := func(id CommitID) {
		if _, ok := s.commits[id]; !ok {
			return
		}
		if _, seen := rs.commits[id]; seen {
			return
		}
		rs.commits[id] = struct{}{}
		queue = append(queue, id)
	}
	for _, tip := range s.refs {
		enqueue(tip)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		cm := s.commits[id]
		for _, b := range cm.Blobs {
			rs.blobs[b] = struct{}{}
		}
		if _, stop := s.boundary[id]; stop {
			continue
		}
		for _, p := range cm.Parents {
			enqueue(p)
		}
	}
	return rs
}
