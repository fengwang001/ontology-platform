package ontology

import "sort"

// slotKey 标识一个「实例上的链接来源属性」基数槽位。
// 同一实例同一来源属性上挂载的全部链接（即使来自不同链接类型）共享一个槽位计数。
type slotKey struct {
	instance string
	property string
}

// Ledger 负责链接基数账本：记录现存链接并独立判定两端基数是否超限。
// 它不关心权限；只根据自身账本状态给出结论。权限收紧不经过账本，
// 因而绝不触发物理删除或基数释放。
type Ledger struct {
	links  map[string]Link
	source map[slotKey]int
	target map[slotKey]int
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{
		links:  make(map[string]Link),
		source: make(map[slotKey]int),
		target: make(map[slotKey]int),
	}
}

// Exists 报告一条链接当前是否存在。
func (l *Ledger) Exists(link Link) bool {
	_, ok := l.links[link.Key()]
	return ok
}

// CountSource 返回某起点实例来源属性上的现存链接数。
func (l *Ledger) CountSource(instance, property string) int {
	return l.source[slotKey{instance, property}]
}

// CountTarget 返回某终点实例来源属性上的现存链接数。
func (l *Ledger) CountTarget(instance, property string) int {
	return l.target[slotKey{instance, property}]
}

// CheckSource / CheckTarget 在「不修改账本」的前提下判定创建该链接后是否超限。
// max <= 0 表示该端无基数上限。
func (l *Ledger) CheckSource(instance, property string, max int) bool {
	if max <= 0 {
		return true
	}
	return l.source[slotKey{instance, property}] < max
}

func (l *Ledger) CheckTarget(instance, property string, max int) bool {
	if max <= 0 {
		return true
	}
	return l.target[slotKey{instance, property}] < max
}

// AddOnSlots 写入链接并按两端来源属性占用基数槽位。
// 已存在同键链接时为幂等空操作，不重复计数。
func (l *Ledger) AddOnSlots(link Link, sourceProperty, targetProperty string) {
	if !l.Exists(link) {
		l.links[link.Key()] = link
		l.source[slotKey{link.SourceInstance, sourceProperty}]++
		l.target[slotKey{link.TargetInstance, targetProperty}]++
	}
}

// Remove 删除一条链接并释放两端基数槽位。不存在时返回 false 且不产生副作用。
func (l *Ledger) Remove(link Link, sourceProperty, targetProperty string) bool {
	if !l.Exists(link) {
		return false
	}
	delete(l.links, link.Key())
	sk := slotKey{link.SourceInstance, sourceProperty}
	tk := slotKey{link.TargetInstance, targetProperty}
	l.source[sk]--
	if l.source[sk] <= 0 {
		delete(l.source, sk)
	}
	l.target[tk]--
	if l.target[tk] <= 0 {
		delete(l.target, tk)
	}
	return true
}

// All 返回现存链接集合的快照，顺序确定（按键排序）以便重放与对照。
func (l *Ledger) All() []Link {
	keys := make([]string, 0, len(l.links))
	for k := range l.links {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Link, 0, len(keys))
	for _, k := range keys {
		out = append(out, l.links[k])
	}
	return out
}
