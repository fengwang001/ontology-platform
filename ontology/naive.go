package ontology

import "sort"

// naiveModel 是对照用的朴素参考实现：
//
//   - 只维护一个全量链接集合，不维护任何增量计数；
//   - 每次校验都遍历当前全量集合重新统计两端计数（开销随链接总数线性增长）；
//   - 错误归类、拒绝次序、批内累积、两语义回滚规则与正式实现逐字一致。
//
// 随机差分测试用它作为「判定依据」：任意操作序列下，正式系统的逐条结论与
// 最终链接集合必须与朴素模型完全相同。
type naiveModel struct {
	decls   map[string]LinkTypeDecl
	objects map[string]string
	links   map[linkKey]struct{}
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		decls:   map[string]LinkTypeDecl{},
		objects: map[string]string{},
		links:   map[linkKey]struct{}{},
	}
}

func (m *naiveModel) registerLinkType(d LinkTypeDecl) {
	if err := d.Validate(); err != nil {
		panic(err)
	}
	m.decls[d.Name] = d
}

func (m *naiveModel) registerObject(id, typ string) { m.objects[id] = typ }

// count 通过遍历全量集合重新统计某实例在某链接类型一侧的当前计数。
func (m *naiveModel) count(linkType, instance string, sourceSide bool) int {
	n := 0
	for k := range m.links {
		if k.linkType != linkType {
			continue
		}
		if sourceSide && k.source == instance {
			n++
		}
		if !sourceSide && k.target == instance {
			n++
		}
	}
	return n
}

func (m *naiveModel) exists(linkType string, p Pair) bool {
	_, ok := m.links[linkKey{linkType, p.Source, p.Target}]
	return ok
}

// validate 与 Service.validateArguments 次序一致；seenPairs 非 nil 时为批次上下文。
func (m *naiveModel) validate(item BatchItem, seenPairs map[Pair]struct{}) (LinkTypeDecl, ErrKind) {
	decl, ok := m.decls[item.LinkType]
	if !ok {
		return LinkTypeDecl{}, KindInvalidArgument
	}
	srcType, srcOK := m.objects[item.Source]
	tgtType, tgtOK := m.objects[item.Target]
	if !srcOK || !tgtOK || srcType != decl.SourceType || tgtType != decl.TargetType {
		return LinkTypeDecl{}, KindInvalidArgument
	}
	if m.exists(item.LinkType, item.Pair) {
		return LinkTypeDecl{}, KindInvalidArgument
	}
	if seenPairs != nil {
		if _, dup := seenPairs[item.Pair]; dup {
			return LinkTypeDecl{}, KindInvalidArgument
		}
	}
	return decl, KindOK
}

// Create 重新计数后判定；结论与错误类别。
func (m *naiveModel) Create(item BatchItem) ErrKind {
	decl, kind := m.validate(item, nil)
	if kind != KindOK {
		return kind
	}
	// 两端独立计数（朴素地重扫两遍全量集合）。
	if cap, limited := decl.SourceCap.Cap(); limited && m.count(item.LinkType, item.Source, true)+1 > cap {
		return KindSourceCardinality
	}
	if cap, limited := decl.TargetCap.Cap(); limited && m.count(item.LinkType, item.Target, false)+1 > cap {
		return KindTargetCardinality
	}
	m.links[linkKey{item.LinkType, item.Source, item.Target}] = struct{}{}
	return KindOK
}

// Delete 删除不存在的链接归一化为 KindNotFound（未知类型同样 NotFound）。
func (m *naiveModel) Delete(item BatchItem) ErrKind {
	if _, ok := m.decls[item.LinkType]; !ok {
		return KindNotFound
	}
	k := linkKey{item.LinkType, item.Source, item.Target}
	if _, ok := m.links[k]; !ok {
		return KindNotFound
	}
	delete(m.links, k)
	return KindOK
}

// Import 与 Service.ImportLinks 的两语义完全一致，差异仅在计数方式。
func (m *naiveModel) Import(mode BatchMode, items []BatchItem) *BatchResult {
	res := &BatchResult{Results: make([]ItemResult, len(items))}
	seen := map[Pair]struct{}{}
	var accepted []int

	for i, item := range items {
		decl, kind := m.validate(item, seen)
		if kind == KindOK {
			if cap, limited := decl.SourceCap.Cap(); limited && m.count(item.LinkType, item.Source, true)+1 > cap {
				kind = KindSourceCardinality
			} else if cap, limited := decl.TargetCap.Cap(); limited && m.count(item.LinkType, item.Target, false)+1 > cap {
				kind = KindTargetCardinality
			}
		}
		if kind != KindOK {
			res.Results[i] = ItemResult{Accepted: false, Kind: kind}
			if mode == AllOrNothing {
				for j := len(accepted) - 1; j >= 0; j-- {
					a := items[accepted[j]]
					delete(m.links, linkKey{a.LinkType, a.Source, a.Target})
				}
				for j := i + 1; j < len(items); j++ {
					res.Results[j] = ItemResult{Accepted: false, Kind: KindInvalidArgument}
				}
				res.Aborted = true
				res.AbortIndex = i
				return res
			}
			continue
		}
		m.links[linkKey{item.LinkType, item.Source, item.Target}] = struct{}{}
		seen[item.Pair] = struct{}{}
		accepted = append(accepted, i)
		res.Results[i] = ItemResult{Accepted: true, Kind: KindOK}
	}
	return res
}

func (m *naiveModel) snapshot() []StoredLink {
	out := make([]StoredLink, 0, len(m.links))
	for k := range m.links {
		out = append(out, StoredLink{LinkType: k.linkType, Pair: Pair{k.source, k.target}})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LinkType != out[j].LinkType {
			return out[i].LinkType < out[j].LinkType
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Target < out[j].Target
	})
	return out
}
