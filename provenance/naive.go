package provenance

import "sort"

// NaiveStore 是独立维护全部历史记录的朴素参照实现，只用于随机差分测试：
//
//   - 不使用任何邻接索引：每次扩展都对「全部链接记录」做线性扫描；
//   - 不共享主实现的判定函数：可见性逻辑在此处用最直白的「遍历全部记录、
//     手工挑最大值」方式重新书写一遍；
//   - 因而它与 Store 若有共同的逻辑错误，大概率不会以同样方式出现，
//     两侧在大量随机操作序列上结果一致即提供强对照。
type NaiveStore struct {
	objects []ObjectRecord
	links   []LinkRecord
}

// NewNaiveStore 创建空的朴素存储。
func NewNaiveStore() *NaiveStore { return &NaiveStore{} }

// WriteObject 追加对象记录，校验规则与 Store.WriteObject 相同。
func (n *NaiveStore) WriteObject(id ObjectID, writeAt Time, valid Interval, exists bool) error {
	if writeAt == IllegalTime {
		return ErrInvalidTime
	}
	if !valid.Valid() {
		return ErrInvalidInterval
	}
	var last Time
	found := false
	for _, r := range n.objects {
		if r.ID == id && (!found || r.WriteAt > last) {
			last, found = r.WriteAt, true
		}
	}
	if found && writeAt <= last {
		return ErrOutOfOrderWrite
	}
	seq := int64(len(n.objects) + len(n.links) + 1)
	n.objects = append(n.objects, ObjectRecord{
		ID: id, WriteAt: writeAt, Valid: normalize(valid, exists), Exists: exists, Seq: seq,
	})
	return nil
}

// WriteLink 追加链接记录，校验规则与 Store.WriteLink 相同。
func (n *NaiveStore) WriteLink(id LinkID, writeAt Time, valid Interval, source, target ObjectID, exists bool) error {
	if writeAt == IllegalTime {
		return ErrInvalidTime
	}
	if !valid.Valid() {
		return ErrInvalidInterval
	}
	var last Time
	found := false
	var firstSrc, firstTgt ObjectID
	for _, r := range n.links {
		if r.ID == id {
			if !found {
				firstSrc, firstTgt = r.Source, r.Target
			}
			if !found || r.WriteAt > last {
				last, found = r.WriteAt, true
			}
		}
	}
	if found {
		if writeAt <= last {
			return ErrOutOfOrderWrite
		}
		if firstSrc != source || firstTgt != target {
			return ErrInvalidInterval
		}
	}
	seq := int64(len(n.objects) + len(n.links) + 1)
	n.links = append(n.links, LinkRecord{
		ID: id, WriteAt: writeAt, Valid: normalize(valid, exists),
		Source: source, Target: target, Exists: exists, Seq: seq,
	})
	return nil
}

// naiveResolveObject 逐条扫描全部对象记录，独立重写 resolveObject 的语义。
func (n *NaiveStore) naiveResolveObject(id ObjectID, validAt, asOf Time) (*ObjectRecord, InvisibleReason) {
	var latest *ObjectRecord // asOf 之前写入时间最大的记录（含墓碑，墓碑不覆盖任何点）
	futureCovering := false
	for i := range n.objects {
		r := &n.objects[i]
		if r.ID != id {
			continue
		}
		if r.WriteAt <= asOf {
			if latest == nil || r.WriteAt > latest.WriteAt ||
				(r.WriteAt == latest.WriteAt && r.Seq > latest.Seq) {
				latest = r
			}
		} else if r.Exists && r.Valid.Contains(validAt) {
			futureCovering = true
		}
	}
	if latest != nil && latest.Exists && latest.Valid.Contains(validAt) {
		return latest, ReasonVisible
	}
	if futureCovering {
		return nil, ReasonNotYetVisible
	}
	return nil, ReasonNotEstablished
}

// naiveResolveLink 逐条扫描全部链接记录。
func (n *NaiveStore) naiveResolveLink(id LinkID, validAt, asOf Time) (*LinkRecord, InvisibleReason) {
	var latest *LinkRecord
	futureCovering := false
	for i := range n.links {
		r := &n.links[i]
		if r.ID != id {
			continue
		}
		if r.WriteAt <= asOf {
			if latest == nil || r.WriteAt > latest.WriteAt ||
				(r.WriteAt == latest.WriteAt && r.Seq > latest.Seq) {
				latest = r
			}
		} else if r.Exists && r.Valid.Contains(validAt) {
			futureCovering = true
		}
	}
	if latest != nil && latest.Exists && latest.Valid.Contains(validAt) {
		return latest, ReasonVisible
	}
	if futureCovering {
		return nil, ReasonNotYetVisible
	}
	return nil, ReasonNotEstablished
}

// distinctLinkIDsFrom 通过全量扫描收集 from 的出链（朴素版不维护邻接表）。
func (n *NaiveStore) distinctLinkIDsFrom(from ObjectID) []LinkID {
	seen := map[LinkID]bool{}
	var ids []LinkID
	for _, r := range n.links {
		if r.Source == from && !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (n *NaiveStore) linkEndpoint(id LinkID) (ObjectID, ObjectID, bool) {
	for _, r := range n.links {
		if r.ID == id {
			return r.Source, r.Target, true
		}
	}
	return "", "", false
}

func (n *NaiveStore) hasObject(id ObjectID) bool {
	for _, r := range n.objects {
		if r.ID == id {
			return true
		}
	}
	return false
}

func (n *NaiveStore) earliestObjectWrite(id ObjectID) (Time, bool) {
	found := false
	var earliest Time
	for _, r := range n.objects {
		if r.ID == id && (!found || r.WriteAt < earliest) {
			earliest, found = r.WriteAt, true
		}
	}
	return earliest, found
}

// Traverse 以全量逐条扫描方式执行与 Store.Traverse 等价的溯源查询，
// 包括相同的固定错误次序与逐跳三方校验。
func (n *NaiveStore) Traverse(q Query) (*Result, error) {
	if !n.hasObject(q.Source) {
		return nil, ErrSourceNotFound
	}
	if !q.ValidAt.Valid() || !q.AsOf.Valid() {
		return nil, ErrInvalidTime
	}
	if q.MaxDepth <= 0 {
		return nil, ErrInvalidDepth
	}
	earliest, _ := n.earliestObjectWrite(q.Source)
	if q.AsOf < earliest {
		return nil, ErrAsOfBeforeSource
	}

	res := &Result{Paths: []Path{}, Rejected: []RejectedHop{}}
	srcRec, srcReason := n.naiveResolveObject(q.Source, q.ValidAt, q.AsOf)
	res.SourceReason = srcReason
	if srcRec != nil {
		res.Source = objectRef(srcRec)
	}
	if srcReason != ReasonVisible {
		return res, nil
	}

	type frontier struct {
		path   Path
		onPath map[ObjectID]bool
	}
	current := []frontier{{
		path:   Path{Nodes: []ObjectID{q.Source}, Hops: []Hop{}},
		onPath: map[ObjectID]bool{q.Source: true},
	}}
	for depth := 1; depth <= q.MaxDepth; depth++ {
		var next []frontier
		for _, fr := range current {
			from := fr.path.Nodes[len(fr.path.Nodes)-1]
			// 朴素实现：不使用邻接索引，每次扩展都线性扫描全部链接记录。
			res.CandidatesSeen += len(n.links)
			for _, lid := range n.distinctLinkIDsFrom(from) {
				lrec, lreason := n.naiveResolveLink(lid, q.ValidAt, q.AsOf)
				_, to, _ := n.linkEndpoint(lid)
				if lreason != ReasonVisible {
					res.Rejected = append(res.Rejected, RejectedHop{
						From: from, Link: lid, To: to, Failed: "link", Reason: lreason,
					})
					continue
				}
				trec, treason := n.naiveResolveObject(to, q.ValidAt, q.AsOf)
				if treason != ReasonVisible {
					res.Rejected = append(res.Rejected, RejectedHop{
						From: from, Link: lid, To: to, Failed: "target", Reason: treason,
					})
					continue
				}
				newPath := Path{
					Nodes: append(append([]ObjectID(nil), fr.path.Nodes...), to),
					Hops: append(append([]Hop(nil), fr.path.Hops...),
						Hop{Link: linkRef(lrec), Target: objectRef(trec), TargetID: to}),
				}
				res.Paths = append(res.Paths, newPath)
				if !fr.onPath[to] && depth < q.MaxDepth {
					nextOnPath := make(map[ObjectID]bool, len(fr.onPath)+1)
					for k := range fr.onPath {
						nextOnPath[k] = true
					}
					nextOnPath[to] = true
					next = append(next, frontier{path: newPath, onPath: nextOnPath})
				}
			}
		}
		current = next
	}
	sortResult(res)
	return res, nil
}
