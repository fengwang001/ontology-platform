package bitemporal

import "sort"

// pairKey 以有序端点标识一条链接事实所涉及的对象对。
type pairKey struct {
	a ID
	b ID
}

// typeKey 承载某个链接类型的元数据与版本列表（发布后不可变）。
type typeKey struct {
	lt    LinkType
	rules []LinkTypeRule // 按 FromRecord 升序
}

// Snapshot 是某一刻存储的不可变视图。
type Snapshot struct {
	gen     int64 // 任何写入都会换版
	ruleGen int64 // 仅当基数约束版本发生变化时换版，用于 E1 判定

	objectBorn map[ID]int64
	types      map[ID]*typeKey
	// facts 按链接类型分组，只追加；发布后的切片不再修改。
	facts map[ID][]linkFact
	// index 为按当前事实重建的只读预计算索引。
	index map[ID]*linkIndex
	// dirty 标记事实已追加但索引尚未重建的链接类型。
	dirty map[ID]bool
}

func newSnapshot() *Snapshot {
	return &Snapshot{
		objectBorn: map[ID]int64{},
		types:      map[ID]*typeKey{},
		facts:      map[ID][]linkFact{},
		index:      map[ID]*linkIndex{},
		dirty:      map[ID]bool{},
	}
}

// clone 为写入复制一份可变快照（深拷贝顶层容器）。
func (s *Snapshot) clone() *Snapshot {
	ns := &Snapshot{
		gen:        s.gen + 1,
		ruleGen:    s.ruleGen,
		objectBorn: make(map[ID]int64, len(s.objectBorn)),
		types:      make(map[ID]*typeKey, len(s.types)),
		facts:      make(map[ID][]linkFact, len(s.facts)),
		index:      make(map[ID]*linkIndex, len(s.index)),
		dirty:      make(map[ID]bool, len(s.dirty)),
	}
	for k, v := range s.objectBorn {
		ns.objectBorn[k] = v
	}
	for k, v := range s.types {
		ns.types[k] = v
	}
	for k, v := range s.facts {
		ns.facts[k] = v
	}
	for k, v := range s.dirty {
		ns.dirty[k] = v
	}
	for k, v := range s.index {
		ns.index[k] = v
	}
	return ns
}

// RuleAt 返回记录时间 rt 生效的基数约束版本（二分查找）。
func (s *Snapshot) RuleAt(linkType ID, rt int64) (LinkTypeRule, bool) {
	tk, ok := s.types[linkType]
	if !ok {
		return LinkTypeRule{}, false
	}
	rules := tk.rules
	i := sort.Search(len(rules), func(i int) bool { return rules[i].FromRecord > rt })
	if i == 0 {
		return LinkTypeRule{}, false
	}
	return rules[i-1], true
}

// ObjectTypeExists 判断对象类型在记录时间 rt 是否已经存在。
func (s *Snapshot) ObjectTypeExists(id ID, rt int64) bool {
	born, ok := s.objectBorn[id]
	return ok && born <= rt
}

// linkType 返回链接类型元数据。
func (s *Snapshot) linkType(id ID) (LinkType, bool) {
	tk, ok := s.types[id]
	if !ok {
		return LinkType{}, false
	}
	return tk.lt, true
}
