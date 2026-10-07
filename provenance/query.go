package provenance

import "sort"

// Query 是一次 AsOf 溯源查询的输入。
type Query struct {
	Source   ObjectID
	ValidAt  Time // 有效时间点
	AsOf     Time // 写入时间点
	MaxDepth int  // 遍历深度上限（正整数）：1 = 仅一跳邻居
}

// RecordRef 指向一条据以判定可见性的具体不可变历史记录。
type RecordRef struct {
	Kind    string `json:"kind"` // "object" | "link"
	ID      string `json:"id"`
	WriteAt Time   `json:"write_at"`
	Seq     int64  `json:"seq"`
}

// Hop 是一条结果路径上的一跳，携带据以判定该跳可见的链接与目标记录。
type Hop struct {
	Link     RecordRef `json:"link"`
	Target   RecordRef `json:"target"`
	TargetID ObjectID  `json:"target_id"`
}

// Path 是从源对象出发的一条完整可见路径（长度 1..MaxDepth 跳均会返回）。
type Path struct {
	Nodes []ObjectID `json:"nodes"` // 长度 = 跳数 + 1，Nodes[0] 为源
	Hops  []Hop      `json:"hops"`
}

// RejectedHop 记录 BFS 中被剪掉的一跳及其失败方与原因类别。
// 失败方判定次序固定：先链接后目标；链接不可见时不再核对目标。
type RejectedHop struct {
	From   ObjectID        `json:"from"`
	Link   LinkID          `json:"link"`
	To     ObjectID        `json:"to"`
	Failed string          `json:"failed"` // "link" | "target"
	Reason InvisibleReason `json:"reason"`
}

// Result 是查询结果。Paths 与 Rejected 均按确定次序排序，
// 保证同一 (ValidAt, AsOf) 下无新写入时重复查询结果逐字节稳定。
type Result struct {
	Source         RecordRef       `json:"source"`
	SourceReason   InvisibleReason `json:"source_reason"`
	Paths          []Path          `json:"paths"`
	Rejected       []RejectedHop   `json:"rejected"`
	CandidatesSeen int             `json:"candidates_seen"` // 本次查询实际触及的候选链接数
}

// Traverse 执行限定深度的多跳双时态溯源。
//
// 错误按固定、互斥的次序判定，只返回第一类，不随内部实现变化：
//
//  1. 源对象实例不存在（存储中无任何该对象的写入历史）；
//  2. ValidAt 或 AsOf 为非法时间值；
//  3. MaxDepth 非正整数；
//  4. AsOf 早于源对象自身最早的写入时间。
//
// 源对象本身不可见（not_established / not_yet_visible）不属于错误：
// 此时返回空 Paths 与 SourceReason，调用方据原因类别区分「未建立」与
// 「尚不可见」。
func (s *Store) Traverse(q Query) (*Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 错误次序 1：源实例不存在。
	if !s.hasObject(q.Source) {
		return nil, ErrSourceNotFound
	}
	// 错误次序 2：非法时间值。
	if !q.ValidAt.Valid() || !q.AsOf.Valid() {
		return nil, ErrInvalidTime
	}
	// 错误次序 3：深度上限非正整数。
	if q.MaxDepth <= 0 {
		return nil, ErrInvalidDepth
	}
	// 错误次序 4：AsOf 早于源对象最早写入时间。
	earliest, _ := s.earliestObjectWrite(q.Source)
	if q.AsOf < earliest {
		return nil, ErrAsOfBeforeSource
	}

	res := &Result{Paths: []Path{}, Rejected: []RejectedHop{}}

	srcRec, srcReason := s.resolveObject(q.Source, q.ValidAt, q.AsOf)
	res.SourceReason = srcReason
	if srcRec != nil {
		res.Source = objectRef(srcRec)
	}
	if srcReason != ReasonVisible {
		// 源自身在此刻不可见：无任何路径可核对，直接返回空集合与原因。
		return res, nil
	}

	// 逐跳 BFS。frontier 中每个条目是一条已逐跳核对通过的前缀路径。
	type frontier struct {
		path   Path
		onPath map[ObjectID]bool // 仅用于阻断同一路径上的环，不做全局去重
	}
	current := []frontier{{
		path:   Path{Nodes: []ObjectID{q.Source}, Hops: []Hop{}},
		onPath: map[ObjectID]bool{q.Source: true},
	}}

	for depth := 1; depth <= q.MaxDepth; depth++ {
		var next []frontier
		// 对每个前缀，取其末端节点的出链；邻接索引保证不扫描全图链接。
		for _, fr := range current {
			from := fr.path.Nodes[len(fr.path.Nodes)-1]
			linkIDs := append([]LinkID(nil), s.out[from]...)
			sort.Slice(linkIDs, func(i, j int) bool { return linkIDs[i] < linkIDs[j] })
			for _, lid := range linkIDs {
				res.CandidatesSeen++
				lrec, lreason := s.resolveLink(lid, q.ValidAt, q.AsOf)
				recs := s.links[lid]
				to := recs[0].Target // 端点不可变，首条记录即权威
				if lreason != ReasonVisible {
					res.Rejected = append(res.Rejected, RejectedHop{
						From: from, Link: lid, To: to, Failed: "link", Reason: lreason,
					})
					continue // 该跳及其之后的延伸一律不计
				}
				trec, treason := s.resolveObject(to, q.ValidAt, q.AsOf)
				if treason != ReasonVisible {
					res.Rejected = append(res.Rejected, RejectedHop{
						From: from, Link: lid, To: to, Failed: "target", Reason: treason,
					})
					continue
				}
				h := Hop{Link: linkRef(lrec), Target: objectRef(trec), TargetID: to}
				newPath := Path{
					Nodes: append(append([]ObjectID(nil), fr.path.Nodes...), to),
					Hops:  append(append([]Hop(nil), fr.path.Hops...), h),
				}
				res.Paths = append(res.Paths, newPath)
				// 该路径在此深度已成为结果；是否继续延伸仅取决于「是否成环」与
				// 「是否还有下一深度」，与当前深度是否等于上限无关。
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

// sortResult 给出与插入/执行次序无关的确定输出顺序：
// 路径按节点序列字典序，拒绝项按 (from, link) 字典序。
func sortResult(res *Result) {
	sort.Slice(res.Paths, func(i, j int) bool {
		a, b := res.Paths[i].Nodes, res.Paths[j].Nodes
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	sort.Slice(res.Rejected, func(i, j int) bool {
		a, b := res.Rejected[i], res.Rejected[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.Link < b.Link
	})
}
