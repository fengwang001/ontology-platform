package ontology

// Ledger 是链接基数账本：只记录物理存在的链接及两端占用数，
// 完全不感知权限语义。
type Ledger struct {
	links   map[string]*Link
	ordered []string
	// srcCount[链接类型][srcID] 与 tgtCount[链接类型][tgtID] 分别维护
	// 起点/终点一侧的占用名额。
	srcCount map[string]map[string]int
	tgtCount map[string]map[string]int
}

// VisBasis 记录一次最终可见性判定所依据的证据，用于打印“据以判定的依据”。
type VisBasis struct {
	InstanceID string
	Attr       string
	Operator   string
	Vis        Visibility
	// Distance 是命中的最近覆盖与操作者之间的层级距离；为 -1 表示退回类型层默认。
	Distance int
	Source   string
	Seq      int
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{
		links:    map[string]*Link{},
		srcCount: map[string]map[string]int{},
		tgtCount: map[string]map[string]int{},
	}
}

// CardVerdict 是账本对“是否还有名额”的独立判定。
type CardVerdict struct {
	SrcOK  bool
	TgtOK  bool
	SrcUse int
	TgtUse int
	SrcMax int
	TgtMax int
}

func (l *Ledger) exists(id string) bool {
	_, ok := l.links[id]
	return ok
}

func (l *Ledger) get(id string) (*Link, bool) {
	lk, ok := l.links[id]
	return lk, ok
}

// check 在不写入的前提下独立判定两端基数是否仍有名额。max <= 0 视为不限。
func (l *Ledger) check(spec LinkTypeSpec, srcID, tgtID string) CardVerdict {
	v := CardVerdict{SrcMax: spec.SrcMax, TgtMax: spec.TgtMax}
	v.SrcUse = l.srcCount[spec.Name][srcID]
	v.TgtUse = l.tgtCount[spec.Name][tgtID]
	v.SrcOK = spec.SrcMax <= 0 || v.SrcUse < spec.SrcMax
	v.TgtOK = spec.TgtMax <= 0 || v.TgtUse < spec.TgtMax
	return v
}

// add 物理写入一条链接并占用两端名额。调用方必须先保证 id 未被占用。
func (l *Ledger) add(lk *Link) {
	l.links[lk.ID] = lk
	l.ordered = append(l.ordered, lk.ID)
	if l.srcCount[lk.TypeName] == nil {
		l.srcCount[lk.TypeName] = map[string]int{}
	}
	if l.tgtCount[lk.TypeName] == nil {
		l.tgtCount[lk.TypeName] = map[string]int{}
	}
	l.srcCount[lk.TypeName][lk.SrcID]++
	l.tgtCount[lk.TypeName][lk.TgtID]++
}

// remove 物理删除一条链接并释放两端名额。
func (l *Ledger) remove(id string) bool {
	lk, ok := l.links[id]
	if !ok {
		return false
	}
	delete(l.links, id)
	for i, x := range l.ordered {
		if x == id {
			l.ordered = append(l.ordered[:i], l.ordered[i+1:]...)
			break
		}
	}
	l.srcCount[lk.TypeName][lk.SrcID]--
	l.tgtCount[lk.TypeName][lk.TgtID]--
	return true
}

// snapshot 返回按 ID 排序的全部物理链接副本，用于测试与重放比对。
func (l *Ledger) snapshot() []Link {
	out := make([]Link, 0, len(l.links))
	for _, lk := range l.links {
		out = append(out, *lk)
	}
	sortLinks(out)
	return out
}
