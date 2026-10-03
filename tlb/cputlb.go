package tlb

import "container/list"

// cpuState 是单个 CPU 的状态：活动对、保留对、刷新待办标记与 TLB。
type cpuState struct {
	active   Pair // ASID 为 0 表示尚无活动上下文
	reserved Pair // ASID 为 0 表示无保留对
	pending  bool
	tlb      cpuTLB
}

// cpuTLB 是单个 CPU 的 TLB：容量有限，最近使用在前。
// ents 提供 O(1) 按键查找；byASID 是逐 CPU 的按 asid 索引，
// 使按 asid 整批删除时不扫描无关条目。
type cpuTLB struct {
	cap    int
	ll     *list.List // 前端为最近使用；Value 类型为 Entry
	ents   map[Key]*list.Element
	byASID map[uint32]map[uint32]*list.Element // asid -> vpn -> 元素
}

func newCPUTLB(capacity int) cpuTLB {
	return cpuTLB{
		cap:    capacity,
		ll:     list.New(),
		ents:   make(map[Key]*list.Element),
		byASID: make(map[uint32]map[uint32]*list.Element),
	}
}

// fill 插入或更新条目并置为最近使用；超过容量时淘汰最久未用者并返回其键。
func (t *cpuTLB) fill(key Key, pfn uint32) (Key, bool, error) {
	if e, ok := t.ents[key]; ok {
		e.Value = Entry{Key: key, PFN: pfn}
		t.ll.MoveToFront(e)
		return Key{}, false, nil
	}
	e := t.ll.PushFront(Entry{Key: key, PFN: pfn})
	t.ents[key] = e
	set, ok := t.byASID[key.ASID]
	if !ok {
		set = make(map[uint32]*list.Element)
		t.byASID[key.ASID] = set
	}
	set[key.VPN] = e
	if t.ll.Len() > t.cap {
		back := t.ll.Back()
		t.remove(back)
		return back.Value.(Entry).Key, true, nil
	}
	return Key{}, false, nil
}

// lookup 命中返回 pfn 并置为最近使用；未命中不改任何状态。
func (t *cpuTLB) lookup(key Key) (uint32, bool, error) {
	e, ok := t.ents[key]
	if !ok {
		return 0, false, nil
	}
	t.ll.MoveToFront(e)
	return e.Value.(Entry).PFN, true, nil
}

// invalidate 删除指定键的条目，返回是否删除。O(1) 单次探测。
func (t *cpuTLB) invalidate(key Key) bool {
	e, ok := t.ents[key]
	if !ok {
		return false
	}
	t.remove(e)
	return true
}

// purgeASID 删除所有标签为 asid 的条目，返回删除条数。
// 只访问该 asid 的条目，不扫描无关条目。
func (t *cpuTLB) purgeASID(asid uint32) int {
	set, ok := t.byASID[asid]
	if !ok {
		return 0
	}
	n := 0
	for _, e := range set {
		t.ll.Remove(e)
		delete(t.ents, e.Value.(Entry).Key)
		n++
	}
	delete(t.byASID, asid)
	return n
}

// clear 清空整个 TLB。
func (t *cpuTLB) clear() {
	t.ll.Init()
	t.ents = make(map[Key]*list.Element)
	t.byASID = make(map[uint32]map[uint32]*list.Element)
}

// entries 按最近使用在前返回全部条目。
func (t *cpuTLB) entries() []Entry {
	out := make([]Entry, 0, t.ll.Len())
	for e := t.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(Entry))
	}
	return out
}

// remove 删除一个元素并维护全部索引。
func (t *cpuTLB) remove(e *list.Element) {
	ent := e.Value.(Entry)
	t.ll.Remove(e)
	delete(t.ents, ent.Key)
	set := t.byASID[ent.Key.ASID]
	delete(set, ent.Key.VPN)
	if len(set) == 0 {
		delete(t.byASID, ent.Key.ASID)
	}
}
