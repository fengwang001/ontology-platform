// Package diff_test 通过随机操作序列对 whiteboard（treap 实现）与
// naive（切片朴素实现）做差分测试：两者必须对同一输入产生同构的
// 错误类别、锁细节与完整快照。
package diff_test

import (
	"fmt"
	"math/rand/v2"

	"ontology/internal/naive"
	"ontology/internal/whiteboard"
)

type opKind int

const (
	opAdd opKind = iota
	opGroup
	opUngroup
	opRemove
	opReorder
	opLock
	opUnlock
	opOrder
	opRank
	opBetween
	opRev
	opKinds
)

type op struct {
	kind    opKind
	user    string
	id      string
	id2     string
	ids     []string
	side    int
	baseRev int64
	ttl     int64
	now     int64
	lo, hi  int
}

type outcome struct {
	errKind int
	holder  string
	remain  int64
	target  string
	order   []string
	rank    int
	rev     int64
}

type generator struct {
	rng    *rand.Rand
	ids    []string // 现存或曾用元素（部分可能已删除）
	alive  []string
	groups []string
	now    int64
	rev    int64
}

func (g *generator) recordID(id string) {
	for _, x := range g.ids {
		if x == id {
			return
		}
	}
	g.ids = append(g.ids, id)
}

func (g *generator) addAlive(id string) {
	g.recordID(id)
	g.alive = append(g.alive, id)
}

func (g *generator) removeAlive(id string) {
	for i, x := range g.alive {
		if x == id {
			g.alive = append(g.alive[:i], g.alive[i+1:]...)
			return
		}
	}
}

func (g *generator) removeGroup(id string) {
	for i, x := range g.groups {
		if x == id {
			g.groups = append(g.groups[:i], g.groups[i+1:]...)
			return
		}
	}
}

// advance 根据已确认被接受/拒绝的结果更新生成器对世界的认识。
// 被拒绝操作零副作用，直接返回；被接受后以白板快照做一次完全重同步，
// 避免生成器与真实状态漂移。
func (g *generator) advance(o op, accepted bool, b *whiteboard.Board) {
	if !accepted {
		return
	}
	switch o.kind {
	case opAdd, opGroup, opUngroup, opRemove, opReorder:
		g.rev = b.Rev()
	}
	if o.kind == opGroup && accepted {
		// 组成员 id 已存在于 alive；这里只登记组名。
		g.groups = append(g.groups, o.id)
	}
	snap := b.Snapshot(clampTime(o.now))
	g.alive = g.alive[:0]
	for id := range snap.Elems {
		g.recordID(id)
		g.alive = append(g.alive, id)
	}
	g.groups = g.groups[:0]
	for id := range snap.Groups {
		g.groups = append(g.groups, id)
	}
}

func pick[E any](rng *rand.Rand, s []E) E {
	return s[rng.IntN(len(s))]
}

func (g *generator) pool() []string {
	return append(append([]string(nil), g.alive...), g.groups...)
}

func users() []string { return []string{"alice", "bob", "carol"} }

// genOp 生成操作；now 单调不减，偶尔故意构造各类非法/冲突输入以提高拒绝路径覆盖。
func (g *generator) genOp(seq int) op {
	rng := g.rng
	// 时间：80% 前进或持平，15% 故意回退，其余注入 now 越界。
	switch x := rng.IntN(20); {
	case x < 14:
		g.now += int64(rng.IntN(4))
	case x < 17:
		// 保持
	default:
		g.now += int64(rng.IntN(3))
	}
	o := op{user: pick(rng, users()), now: g.now}
	kind := opKind(rng.IntN(int(opKinds)))
	o.kind = kind
	switch kind {
	case opAdd:
		if rng.IntN(5) == 0 && len(g.ids) > 0 {
			o.id = pick(rng, g.ids) // 可能撞已有 id
		} else {
			o.id = fmt.Sprintf("e%d", seq)
		}
	case opGroup:
		n := 2 + rng.IntN(4)
		avail := append([]string(nil), g.alive...)
		rng.Shuffle(len(avail), func(i, j int) { avail[i], avail[j] = avail[j], avail[i] })
		if len(avail) > n {
			avail = avail[:n]
		}
		if len(o.ids) < 2 {
			o.kind = opAdd
			o.id = fmt.Sprintf("e%d", seq)
			o.ids = nil
			break
		}
		o.id = fmt.Sprintf("grp%d", seq)
		if rng.IntN(8) == 0 && len(g.ids) > 0 {
			o.id = pick(rng, append(append([]string(nil), g.ids...), g.groups...))
		}
	case opUngroup, opRemove, opLock, opUnlock, opRank:
		pool := g.pool()
		if len(pool) == 0 {
			o.kind = opAdd
			o.id = fmt.Sprintf("e%d", seq)
			break
		}
		o.id = pick(rng, pool)
		if kind == opLock {
			o.ttl = int64(1 + rng.IntN(12))
		}
	case opReorder:
		pool := g.pool()
		if len(pool) < 2 {
			o.kind = opAdd
			o.id = fmt.Sprintf("e%d", seq)
			break
		}
		o.id = pick(rng, pool)
		o.id2 = pick(rng, pool)
		if rng.IntN(2) == 0 {
			o.side = 1
		} else {
			o.side = -1
		}
		// baseRev：多数使用最新 rev（通过），少数故意过期以制造冲突
		if rng.IntN(3) == 0 {
			o.baseRev = int64(rng.IntN(int(g.rev) + 2))
		} else {
			o.baseRev = g.rev
		}
	case opBetween:
		if len(g.alive) == 0 {
			o.kind = opAdd
			o.id = fmt.Sprintf("e%d", seq)
			break
		}
		n := len(g.alive)
		o.lo = 1 + rng.IntN(n)
		o.hi = o.lo + rng.IntN(n-o.lo+1)
	}
	// 低频注入明显非法参数。
	if rng.IntN(25) == 0 {
		o.user = ""
	}
	if rng.IntN(40) == 0 {
		o.now = -1
	}
	return o
}

func runWb(b *whiteboard.Board, o op) outcome {
	var out outcome
	var err error
	switch o.kind {
	case opAdd:
		err = b.Add(o.user, o.id, o.now)
	case opGroup:
		err = b.Group(o.user, o.id, o.ids, o.now)
	case opUngroup:
		err = b.Ungroup(o.user, o.id, o.now)
	case opRemove:
		err = b.Remove(o.user, o.id, o.now)
	case opReorder:
		err = b.Reorder(o.user, o.id, o.id2, whiteboard.Side(o.side), o.baseRev, o.now)
	case opLock:
		err = b.Lock(o.user, o.id, o.ttl, o.now)
	case opUnlock:
		err = b.Unlock(o.user, o.id, o.now)
	case opOrder:
		out.order = b.Order()
	case opRank:
		out.rank, err = b.Rank(o.id)
	case opBetween:
		out.order, err = b.Between(o.lo, o.hi)
	case opRev:
		out.rev = b.Rev()
	}
	fillErr(&out, err)
	return out
}

func runNb(b *naive.Board, o op) outcome {
	var out outcome
	var err error
	switch o.kind {
	case opAdd:
		err = b.Add(o.user, o.id, o.now)
	case opGroup:
		err = b.Group(o.user, o.id, o.ids, o.now)
	case opUngroup:
		err = b.Ungroup(o.user, o.id, o.now)
	case opRemove:
		err = b.Remove(o.user, o.id, o.now)
	case opReorder:
		err = b.Reorder(o.user, o.id, o.id2, naive.Side(o.side), o.baseRev, o.now)
	case opLock:
		err = b.Lock(o.user, o.id, o.ttl, o.now)
	case opUnlock:
		err = b.Unlock(o.user, o.id, o.now)
	case opOrder:
		out.order = b.Order()
	case opRank:
		out.rank, err = b.Rank(o.id)
	case opBetween:
		out.order, err = b.Between(o.lo, o.hi)
	case opRev:
		out.rev = b.Rev()
	}
	fillNaiveErr(&out, err)
	return out
}

func fillErr(out *outcome, err error) {
	if err == nil {
		return
	}
	if le, ok := err.(*whiteboard.LockError); ok {
		out.errKind = int(whiteboard.KindLocked)
		out.holder, out.remain, out.target = le.Holder, le.Remain, le.TargetID
		return
	}
	if re, ok := err.(*whiteboard.RejectError); ok {
		out.errKind = int(re.Kind)
	}
}

func fillNaiveErr(out *outcome, err error) {
	if err == nil {
		return
	}
	if e, ok := err.(*naive.Error); ok {
		out.errKind = int(e.Kind)
		out.holder, out.remain, out.target = e.Holder, e.Remain, e.TargetID
	}
}
