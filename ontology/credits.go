package ontology

import "math/rand"

// credit 是一笔存入的抵扣额度。
type credit struct {
	id      int64 // 全局存入序号，决定同到期月内 FIFO
	deposit MonthKey
	expiry  MonthKey
	amount  int64 // 剩余可抵扣电量（度）
}

// creditStore 以 (到期月, 存入序号) 为键的 treap，
// 支撑按到期顺序使用、整月到期付款与按笔删除，均不触碰已用尽/已到期笔数。
type creditStore struct {
	root *ctNode
	next int64
}

type ctNode struct {
	c     *credit
	left  *ctNode
	right *ctNode
	prio  uint64
}

func (s *creditStore) deposit(deposit, expiry MonthKey, amount int64) *credit {
	if amount <= 0 {
		return nil
	}
	s.next++
	c := &credit{id: s.next, deposit: deposit, expiry: expiry, amount: amount}
	l, r := ctSplit(s.root, expiry, c.id)
	s.root = ctMerge(ctMerge(l, &ctNode{c: c, prio: rand.Uint64()}), r)
	return c
}

// use 按到期月升序、同到期月按存入月升序消费至多 want 度，返回实际消费量。
// 只使用到期月不早于 minExpiry 的额度（到期月恰等于封账月的额度可在当月抵扣，之后才到期）。
func (s *creditStore) use(minExpiry MonthKey, want int64) int64 {
	used := int64(0)
	var dead []*ctNode
	// 切出到期月 >= minExpiry 的子树（id 从 1 开始，故键 < (minExpiry, 0) 恰为到期月更早者）。
	l, work := ctSplit(s.root, minExpiry, 0)
	var walk func(t *ctNode)
	walk = func(t *ctNode) {
		if t == nil || used >= want {
			return
		}
		walk(t.left)
		if used >= want {
			return
		}
		take := want - used
		if take > t.c.amount {
			take = t.c.amount
		}
		t.c.amount -= take
		used += take
		if t.c.amount == 0 {
			dead = append(dead, t)
		}
		walk(t.right)
	}
	walk(work)
	// 物理删除被用尽的笔：按 id 逐个 erase。笔数只与本次触碰笔数同阶。
	for _, d := range dead {
		work = ctErase(work, d.c.expiry, d.c.id)
	}
	s.root = ctMerge(l, work)
	return used
}

// expire 清零到期月 <= m 的全部余额并返回总电量。
func (s *creditStore) expire(m MonthKey) int64 {
	l, r := ctSplit(s.root, m, 1<<62)
	total := int64(0)
	var sum func(t *ctNode)
	sum = func(t *ctNode) {
		if t == nil {
			return
		}
		total += t.c.amount
		sum(t.left)
		sum(t.right)
	}
	sum(l)
	s.root = r // 到期笔物理摘除，后续操作永不遍历它们
	return total
}

func (s *creditStore) balance() int64 {
	total := int64(0)
	var sum func(t *ctNode)
	sum = func(t *ctNode) {
		if t == nil {
			return
		}
		total += t.c.amount
		sum(t.left)
		sum(t.right)
	}
	sum(s.root)
	return total
}

// snapshot 返回结构与余额的深拷贝，供未封账月预览时零副作用地模拟额度消费。
func (s *creditStore) snapshot() creditStore {
	var cp func(t *ctNode) *ctNode
	cp = func(t *ctNode) *ctNode {
		if t == nil {
			return nil
		}
		c := *t.c
		return &ctNode{c: &c, left: cp(t.left), right: cp(t.right), prio: t.prio}
	}
	return creditStore{root: cp(s.root), next: s.next}
}

// splitBefore 返回（键 < key 的树, 键 >= key 的树）。
func ctSplit(t *ctNode, expiry MonthKey, id int64) (*ctNode, *ctNode) {
	if t == nil {
		return nil, nil
	}
	if ctLess(t.c.expiry, t.c.id, expiry, id) {
		l, r := ctSplit(t.right, expiry, id)
		t.right = l
		return t, r
	}
	l, r := ctSplit(t.left, expiry, id)
	t.left = r
	return l, t
}

func ctMerge(a, b *ctNode) *ctNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = ctMerge(a.right, b)
		return a
	}
	b.left = ctMerge(a, b.left)
	return b
}

func ctErase(t *ctNode, expiry MonthKey, id int64) *ctNode {
	if t == nil {
		return nil
	}
	if ctLess(expiry, id, t.c.expiry, t.c.id) {
		t.left = ctErase(t.left, expiry, id)
		return t
	}
	if ctLess(t.c.expiry, t.c.id, expiry, id) {
		t.right = ctErase(t.right, expiry, id)
		return t
	}
	return ctMerge(t.left, t.right)
}

func ctLess(expA MonthKey, idA int64, expB MonthKey, idB int64) bool {
	if expA != expB {
		return expA < expB
	}
	return idA < idB
}
