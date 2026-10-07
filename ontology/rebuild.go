package ontology

import "sort"

// computeSnapshot 从全部声明出发，用不动点迭代全量重算标签继承关系。
//
// 传播语义：
//   - 直接挂载 (Attachment) 是源头；
//   - 若对象类型 X 携带标签 T，存在传播声明 (T, L, Downstream) 且存在
//     边 (L, X, Y)，则 Y 携带 T；Upstream 则沿边反向传播；
//   - 阻断点 (X, T) 使 X 不再向外传播 T，但 X 自身仍携带 T；
//   - 迭代空间为有限的 (对象类型 × 标签 × 来源 × 传播声明子集) 且单调
//     增长，因此即使关系图含环也必然终止，结果与路径枚举顺序无关。
//
// 同时计算 shadow 视图：忽略全部阻断点时的可达性，用于在判定时区分
// “标签的全部继承路径被阻断”与“标签本就不会到达该对象类型”。
func computeSnapshot(decl *declarations) *snapshot {
	propsByTag := map[string][]Propagation{}
	for p := range decl.propagations {
		propsByTag[p.Tag] = append(propsByTag[p.Tag], p)
	}
	// 出边索引：按链接类型与方向预分组，传播时只访问当前节点的邻边。
	outDown := map[string][]LinkEdge{} // linkType -> 边（按 From 出发）
	outUp := map[string][]LinkEdge{}   // linkType -> 边（按 To 出发）
	for e := range decl.edges {
		outDown[e.LinkType] = append(outDown[e.LinkType], e)
		outUp[e.LinkType] = append(outUp[e.LinkType], e)
	}
	neighbors := func(obj, linkType string, dir Direction, yield func(string)) {
		if dir == Downstream {
			for _, e := range outDown[linkType] {
				if e.From == obj {
					yield(e.To)
				}
			}
			return
		}
		for _, e := range outUp[linkType] {
			if e.To == obj {
				yield(e.From)
			}
		}
	}

	// carried[obj][tag][attachment] = 传播途中使用过的传播声明集合。
	// 工作队列不动点：仅当某 (对象类型, 标签) 的来源集合增长时才重新
	// 外推；状态空间有限且单调增长，含环图也必然终止。
	carried := map[string]map[string]map[Attachment]map[Propagation]bool{}
	type carryKey struct {
		obj string
		tag string
	}
	var queue []carryKey
	queued := map[carryKey]bool{}
	enqueue := func(obj, tag string) {
		k := carryKey{obj, tag}
		if !queued[k] {
			queued[k] = true
			queue = append(queue, k)
		}
	}
	addSource := func(obj, tag string, att Attachment, via map[Propagation]bool) {
		tags, ok := carried[obj]
		if !ok {
			tags = map[string]map[Attachment]map[Propagation]bool{}
			carried[obj] = tags
		}
		srcs, ok := tags[tag]
		if !ok {
			srcs = map[Attachment]map[Propagation]bool{}
			tags[tag] = srcs
		}
		set, ok := srcs[att]
		changed := !ok
		if !ok {
			set = map[Propagation]bool{}
			srcs[att] = set
		}
		for p := range via {
			if !set[p] {
				set[p] = true
				changed = true
			}
		}
		if changed {
			enqueue(obj, tag)
		}
	}
	for att := range decl.attachments {
		addSource(att.ObjectType, att.Tag, att, map[Propagation]bool{})
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		delete(queued, k)
		if decl.blocks[Block{ObjectType: k.obj, Tag: k.tag}] {
			continue // 阻断点：不再向外传播，自身仍携带
		}
		srcs := carried[k.obj][k.tag]
		for _, p := range propsByTag[k.tag] {
			neighbors(k.obj, p.LinkType, p.Direction, func(next string) {
				for att, via := range srcs {
					extended := make(map[Propagation]bool, len(via)+1)
					for q := range via {
						extended[q] = true
					}
					extended[p] = true
					addSource(next, k.tag, att, extended)
				}
			})
		}
	}

	// shadow：忽略阻断点的布尔可达性，同样用工作队列计算。
	shadow := map[string]map[string]bool{}
	reach := func(obj, tag string) {
		tags, ok := shadow[obj]
		if !ok {
			tags = map[string]bool{}
			shadow[obj] = tags
		}
		if !tags[tag] {
			tags[tag] = true
			enqueue(obj, tag)
		}
	}
	for att := range decl.attachments {
		reach(att.ObjectType, att.Tag)
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		delete(queued, k)
		for _, p := range propsByTag[k.tag] {
			neighbors(k.obj, p.LinkType, p.Direction, func(next string) {
				reach(next, k.tag)
			})
		}
	}

	grantsByRole := map[string][]Grant{}
	for g := range decl.grants {
		grantsByRole[g.Role] = append(grantsByRole[g.Role], g)
	}
	for _, gs := range grantsByRole {
		sort.Slice(gs, func(i, j int) bool {
			if gs[i].Tag != gs[j].Tag {
				return gs[i].Tag < gs[j].Tag
			}
			return gs[i].Effect < gs[j].Effect
		})
	}

	return &snapshot{carried: convertCarried(carried), shadow: shadow, grantsByRole: grantsByRole}
}

// convertCarried 把内部表示转换为排序后的、输出稳定的物化视图。
func convertCarried(in map[string]map[string]map[Attachment]map[Propagation]bool) map[string]map[string][]TagSource {
	out := map[string]map[string][]TagSource{}
	for obj, tags := range in {
		out[obj] = map[string][]TagSource{}
		for tag, srcs := range tags {
			list := make([]TagSource, 0, len(srcs))
			for att, via := range srcs {
				props := make([]Propagation, 0, len(via))
				for p := range via {
					props = append(props, p)
				}
				sort.Slice(props, func(i, j int) bool {
					if props[i].LinkType != props[j].LinkType {
						return props[i].LinkType < props[j].LinkType
					}
					return props[i].Direction < props[j].Direction
				})
				list = append(list, TagSource{Attachment: att, Via: props})
			}
			sort.Slice(list, func(i, j int) bool {
				if list[i].Attachment.ObjectType != list[j].Attachment.ObjectType {
					return list[i].Attachment.ObjectType < list[j].Attachment.ObjectType
				}
				return list[i].Attachment.Tag < list[j].Attachment.Tag
			})
			out[obj][tag] = list
		}
	}
	return out
}
