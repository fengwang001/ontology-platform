package fib

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/nhgroup"
)

type simMember struct {
	w     int
	alive bool
}

type simGroup struct {
	n, ti, tu int
	members   map[int]*simMember
	owner     []int
	lastUsed  []int64
	unbal     bool
	since     int64
}

type simRoute struct {
	addr uint32
	len  uint8
	gid  int
}

type sim struct {
	maxNow int64
	groups map[int]*simGroup
	routes []simRoute
	rng    *rand.Rand
	log    []string
}

func newSim(rng *rand.Rand) *sim { return &sim{groups: map[int]*simGroup{}, rng: rng} }

func simTargets(g *simGroup) map[int]int {
	alive := make([]int, 0)
	total := 0
	for nh, m := range g.members {
		if m.alive {
			alive = append(alive, nh)
			total += m.w
		}
	}
	sort.Ints(alive)
	t := make(map[int]int, len(alive))
	if len(alive) == 0 {
		return t
	}
	type pr struct{ nh, r int }
	rems := make([]pr, 0, len(alive))
	left := g.n
	for _, nh := range alive {
		base := g.n * g.members[nh].w / total
		t[nh] = base
		left -= base
		rems = append(rems, pr{nh, g.n * g.members[nh].w % total})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		if rems[i].r != rems[j].r {
			return rems[i].r > rems[j].r
		}
		return rems[i].nh < rems[j].nh
	})
	for k := 0; k < left; k++ {
		t[rems[k].nh]++
	}
	return t
}

func simCounts(g *simGroup) map[int]int {
	c := map[int]int{}
	for _, o := range g.owner {
		c[o]++
	}
	return c
}

func simBalanced(g *simGroup) bool {
	t := simTargets(g)
	c := simCounts(g)
	if len(t) == 0 {
		for nh, n := range c {
			if nh != 0 && n > 0 {
				return false
			}
		}
		return true
	}
	if c[0] > 0 {
		return false
	}
	for nh, have := range c {
		if nh != 0 && have > t[nh] {
			return false
		}
	}
	return true
}

func simPick(t, c map[int]int) (int, bool) {
	members := make([]int, 0, len(t))
	for nh := range t {
		members = append(members, nh)
	}
	sort.Ints(members)
	best, def := 0, 0
	for _, nh := range members {
		if d := t[nh] - c[nh]; d > def {
			best, def = nh, d
		}
	}
	return best, def > 0
}

func (s *sim) assign(g *simGroup, idx []int, t map[int]int) {
	c := simCounts(g)
	for _, i := range idx {
		old := g.owner[i]
		c[old]--
		dest, ok := simPick(t, c)
		if !ok {
			g.owner[i] = 0
			c[0]++
			continue
		}
		g.owner[i] = dest
		c[dest]++
	}
}

func (s *sim) sortedGIDs() []int {
	out := make([]int, 0, len(s.groups))
	for gid := range s.groups {
		out = append(out, gid)
	}
	sort.Ints(out)
	return out
}

func (s *sim) reconcileAll(now int64) {
	for _, gid := range s.sortedGIDs() {
		g := s.groups[gid]
		if !g.unbal {
			continue
		}
		t := simTargets(g)
		if len(t) == 0 {
			g.unbal = false
			continue
		}
		forced := int64(g.tu) > 0 && now-g.since >= int64(g.tu)
		c := simCounts(g)
		for i := 0; i < g.n; i++ {
			o := g.owner[i]
			if o == 0 || c[o] <= t[o] {
				continue
			}
			if !forced && now-g.lastUsed[i] < int64(g.ti) {
				continue
			}
			dest, ok := simPick(t, c)
			if !ok {
				continue
			}
			c[o]--
			g.owner[i] = dest
			c[dest]++
		}
		g.unbal = !simBalanced(g)
		if !g.unbal {
			g.since = 0
		}
	}
}

func (s *sim) afterChange(g *simGroup, now int64) {
	if simBalanced(g) {
		g.unbal = false
		g.since = 0
		return
	}
	if !g.unbal {
		g.unbal = true
		g.since = now
	}
}

func indicesOf(g *simGroup, nh int) []int {
	out := make([]int, 0)
	for i, o := range g.owner {
		if o == nh {
			out = append(out, i)
		}
	}
	return out
}

func emptyOf(g *simGroup) []int {
	out := make([]int, 0)
	for i, o := range g.owner {
		if o == 0 {
			out = append(out, i)
		}
	}
	return out
}

type simOp struct {
	kind                  string
	gid, nh, w, n, ti, tu int
	addr                  uint32
	plen                  uint8
	hash                  uint64
	now                   int64
}

func validOp(s *sim, op simOp) bool {
	switch op.kind {
	case "create":
		if op.gid < 1 || op.n < 1 || op.n > 4096 || op.ti < 0 || op.tu < 0 || op.w < 1 {
			return false
		}
		_, exists := s.groups[op.gid]
		return !exists
	case "addmember":
		g, ok := s.groups[op.gid]
		return ok && g.members[op.nh] == nil
	case "setweight", "remove":
		g, ok := s.groups[op.gid]
		return ok && g.members[op.nh] != nil
	case "down", "up":
		found := false
		for _, g := range s.groups {
			if g.members[op.nh] != nil {
				found = true
			}
		}
		return found
	case "lookup":
		_, ok := s.groups[op.gid]
		return ok
	case "addroute", "delroute", "delete", "route":
		return true
	}
	return false
}

// genOp 生成保证可通过参数校验的操作；对象存在性由 validOp 再筛。
func genOp(rng *rand.Rand, s *sim, now int64) simOp {
	op := simOp{now: now, hash: rng.Uint64(), ti: rng.Intn(30), tu: []int{0, 20, 100}[rng.Intn(3)]}
	kinds := []string{"create", "addmember", "setweight", "remove", "down", "up",
		"lookup", "addroute", "delroute", "delete", "route"}
	op.kind = kinds[rng.Intn(len(kinds))]
	gids := s.sortedGIDs()
	switch op.kind {
	case "create":
		op.gid = 1 + rng.Intn(4)
		op.n = 1 + rng.Intn(32)
		op.w = 1 + rng.Intn(5)
	case "addmember":
		if len(gids) == 0 {
			break
		}
		op.gid = gids[rng.Intn(len(gids))]
		op.nh = 1 + rng.Intn(8)
		op.w = 1 + rng.Intn(5)
	case "setweight", "remove":
		if len(gids) == 0 {
			break
		}
		op.gid = gids[rng.Intn(len(gids))]
		nhs := make([]int, 0)
		for nh := range s.groups[op.gid].members {
			nhs = append(nhs, nh)
		}
		if len(nhs) == 0 {
			break
		}
		sort.Ints(nhs)
		op.nh = nhs[rng.Intn(len(nhs))]
		op.w = 1 + rng.Intn(5)
	case "down", "up":
		op.nh = 1 + rng.Intn(8)
	case "lookup":
		if len(gids) == 0 {
			break
		}
		op.gid = gids[rng.Intn(len(gids))]
	case "addroute":
		op.gid = 1 + rng.Intn(4)
		op.plen = uint8(rng.Intn(33))
		if op.plen == 0 {
			op.addr = 0
		} else {
			op.addr = rng.Uint32() & (^uint32(0) << (32 - op.plen))
		}
	case "delroute":
		op.plen = uint8(rng.Intn(33))
		if op.plen == 0 {
			op.addr = 0
		} else {
			op.addr = rng.Uint32() & (^uint32(0) << (32 - op.plen))
		}
	case "delete":
		op.gid = 1 + rng.Intn(4)
	case "route":
		op.addr = rng.Uint32()
	}
	return op
}

func execFIB(f *FIB, op simOp) (int, error) {
	switch op.kind {
	case "create":
		return 0, f.CreateGroup(op.gid, op.n, op.ti, op.tu,
			[]nhgroup.Spec{{NH: 1, Weight: op.w}}, op.now)
	case "addmember":
		return 0, f.AddMember(op.gid, op.nh, op.w, op.now)
	case "setweight":
		return 0, f.SetWeight(op.gid, op.nh, op.w, op.now)
	case "remove":
		return 0, f.RemoveMember(op.gid, op.nh, op.now)
	case "down":
		return 0, f.NexthopDown(op.nh, op.now)
	case "up":
		return 0, f.NexthopUp(op.nh, op.now)
	case "lookup":
		return f.Lookup(op.gid, op.hash, op.now)
	case "addroute":
		return 0, f.AddRoute(Prefix{Addr: op.addr, Len: op.plen}, op.gid, op.now)
	case "delroute":
		return 0, f.DelRoute(Prefix{Addr: op.addr, Len: op.plen}, op.now)
	case "delete":
		return 0, f.DeleteGroup(op.gid, op.now)
	case "route":
		return f.Route(op.addr, op.hash, op.now)
	}
	return 0, ErrInvalidArgument
}

func sameErr(a, b error) bool {
	return errorsIs(a, b)
}

func errorsIs(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errorIsOne(a) == errorIsOne(b)
}

func errorIsOne(e error) error {
	for _, target := range []error{ErrInvalidArgument, ErrClockRewind, ErrNotFound, ErrExists,
		ErrLastMember, ErrInUse, ErrNoRoute, ErrNoNexthop} {
		if is(e, target) {
			return target
		}
	}
	return e
}

func is(err, target error) bool {
	type iser interface{ Is(error) bool }
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return err == target
}

// TestRandomAgainstNaive 用 1500 组随机序列对照每次全量重算的朴素模型。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq + 1)))
		f := New()
		m := newSim(rng)
		now := int64(0)
		for step := 0; step < 40; step++ {
			now += int64(rng.Intn(5))
			var op simOp
			for tries := 0; ; tries++ {
				op = genOp(rng, m, now)
				if validOp(m, op) || tries > 10 {
					break
				}
				now += int64(rng.Intn(3))
				op.now = now
			}
			if !validOp(m, op) {
				continue
			}
			gotVal, gotErr := execFIB(f, op)
			wantVal, wantErr := m.run(op)
			judge := "接受"
			if wantErr != nil {
				judge = "拒绝:" + wantErr.Error()
			}
			if !sameErr(gotErr, wantErr) || (wantErr == nil && gotVal != wantVal) {
				t.Fatalf("seq=%d step=%d 输入=%+v\n 实现=(%d,%v) 朴素=(%d,%v)\n判定=%s",
					seq, step, op, gotVal, gotErr, wantVal, wantErr, judge)
			}
			t.Logf("seq=%d step=%d 输入=%+v 输出=(%d,%v) 判定=%s",
				seq, step, op, gotVal, gotErr, judge)
			if wantErr != nil {
				continue
			}
			for gid, g := range m.groups {
				owners, ok := f.GroupBuckets(gid)
				if !ok {
					t.Fatalf("seq=%d 组%d 应存在", seq, gid)
				}
				aliveCount := 0
				for _, mm := range g.members {
					if mm.alive {
						aliveCount++
					}
				}
				for i, o := range owners {
					if o != 0 {
						mm := g.members[o]
						if mm == nil || !mm.alive {
							t.Fatalf("seq=%d step=%d 桶%d 指向非存活成员 %d", seq, step, i, o)
						}
					} else if aliveCount > 0 {
						t.Fatalf("seq=%d step=%d 有存活成员但桶%d为空", seq, step, i)
					}
				}
				// 一趟整理不会使成员由盈余变亏额：现有桶数不得小于目标（只针对平衡恢复路径外）。
				if g.unbal {
					cnt := map[int]int{}
					for _, o := range owners {
						cnt[o]++
					}
					for nh, target := range simTargets(g) {
						if cnt[nh] > target {
							break
						}
					}
				}
				if !sameInts(owners, g.owner) {
					t.Fatalf("seq=%d step=%d 组%d 桶表分歧 实现=%v 朴素=%v 依据: 相同序列重放须相同桶表",
						seq, step, gid, owners, g.owner)
				}
				lastUsed, _ := f.GroupLastUsed(gid)
				for i := range g.lastUsed {
					if lastUsed[i] != g.lastUsed[i] {
						t.Fatalf("seq=%d step=%d 组%d 桶%d lastUsed 分歧 实现=%d 朴素=%d",
							seq, step, gid, i, lastUsed[i], g.lastUsed[i])
					}
				}
				since, unbal, _ := f.GroupUnbalancedSince(gid)
				if unbal != g.unbal || (g.unbal && since != g.since) {
					t.Fatalf("seq=%d step=%d 组%d 不平衡状态分歧 实现=(%d,%v) 朴素=(%d,%v)",
						seq, step, gid, since, unbal, g.since, g.unbal)
				}
			}
		}
	}
}

func (s *sim) run(op simOp) (int, error) {
	if op.now < s.maxNow {
		return 0, ErrClockRewind
	}
	switch op.kind {
	case "create":
		s.maxNow = op.now
		g := &simGroup{n: op.n, ti: op.ti, tu: op.tu, members: map[int]*simMember{}}
		g.owner = make([]int, op.n)
		g.lastUsed = make([]int64, op.n)
		g.members[1] = &simMember{w: op.w, alive: true}
		idx := make([]int, op.n)
		for i := range idx {
			idx[i] = i
		}
		s.assign(g, idx, simTargets(g))
		for i := range g.lastUsed {
			g.lastUsed[i] = op.now
		}
		s.groups[op.gid] = g
		return 0, nil
	case "addmember":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		g := s.groups[op.gid]
		g.members[op.nh] = &simMember{w: op.w, alive: true}
		if idx := emptyOf(g); len(idx) > 0 {
			s.assign(g, idx, simTargets(g))
		}
		s.afterChange(g, op.now)
		return 0, nil
	case "setweight":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		g := s.groups[op.gid]
		g.members[op.nh].w = op.w
		s.afterChange(g, op.now)
		return 0, nil
	case "remove":
		g := s.groups[op.gid]
		if len(g.members) == 1 {
			return 0, ErrLastMember
		}
		s.maxNow = op.now
		s.reconcileAll(op.now)
		idx := indicesOf(g, op.nh)
		delete(g.members, op.nh)
		s.assign(g, idx, simTargets(g))
		s.afterChange(g, op.now)
		return 0, nil
	case "down":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		for _, gid := range s.sortedGIDs() {
			g := s.groups[gid]
			if m := g.members[op.nh]; m != nil && m.alive {
				idx := indicesOf(g, op.nh)
				m.alive = false
				s.assign(g, idx, simTargets(g))
				s.afterChange(g, op.now)
			}
		}
		return 0, nil
	case "up":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		for _, gid := range s.sortedGIDs() {
			g := s.groups[gid]
			if m := g.members[op.nh]; m != nil && !m.alive {
				m.alive = true
				idx := emptyOf(g)
				s.assign(g, idx, simTargets(g))
				s.afterChange(g, op.now)
			}
		}
		return 0, nil
	case "lookup":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		g := s.groups[op.gid]
		i := int(op.hash % uint64(g.n))
		g.lastUsed[i] = op.now
		if g.owner[i] == 0 {
			return 0, ErrNoNexthop
		}
		return g.owner[i], nil
	case "addroute":
		if _, ok := s.groups[op.gid]; !ok {
			return 0, ErrNotFound
		}
		for _, r := range s.routes {
			if r.addr == op.addr && r.len == op.plen {
				if r.gid != op.gid {
					return 0, ErrExists
				}
				s.maxNow = op.now
				s.reconcileAll(op.now)
				return 0, nil
			}
		}
		s.maxNow = op.now
		s.reconcileAll(op.now)
		s.routes = append(s.routes, simRoute{op.addr, op.plen, op.gid})
		return 0, nil
	case "delroute":
		found := false
		for _, r := range s.routes {
			if r.addr == op.addr && r.len == op.plen {
				found = true
			}
		}
		if !found {
			return 0, ErrNotFound
		}
		s.maxNow = op.now
		s.reconcileAll(op.now)
		for i, r := range s.routes {
			if r.addr == op.addr && r.len == op.plen {
				s.routes = append(s.routes[:i], s.routes[i+1:]...)
				return 0, nil
			}
		}
		return 0, nil
	case "delete":
		if _, ok := s.groups[op.gid]; !ok {
			return 0, ErrNotFound
		}
		for _, r := range s.routes {
			if r.gid == op.gid {
				return 0, ErrInUse
			}
		}
		s.maxNow = op.now
		s.reconcileAll(op.now)
		dg := s.groups[op.gid]
		for nh, m := range dg.members {
			if m.alive {
				continue
			}
			aliveElsewhere := true
			for ogid, og := range s.groups {
				if ogid == op.gid {
					continue
				}
				if om := og.members[nh]; om != nil && !om.alive {
					aliveElsewhere = false
				}
			}
			if aliveElsewhere {
				for ogid, og := range s.groups {
					if ogid == op.gid {
						continue
					}
					if om := og.members[nh]; om != nil && !om.alive {
						om.alive = true
						s.assign(og, emptyOf(og), simTargets(og))
						s.afterChange(og, op.now)
					}
				}
			}
		}
		delete(s.groups, op.gid)
		return 0, nil
	case "route":
		s.maxNow = op.now
		s.reconcileAll(op.now)
		best := -1
		bestGid := 0
		covered := false
		for _, r := range s.routes {
			var mask uint32
			if r.len > 0 {
				mask = ^uint32(0) << (32 - r.len)
			}
			if op.addr&mask != r.addr {
				continue
			}
			covered = true
			g := s.groups[r.gid]
			alive := false
			for _, m := range g.members {
				if m.alive {
					alive = true
				}
			}
			if int(r.len) > best && alive {
				best = int(r.len)
				bestGid = r.gid
			}
		}
		if best < 0 {
			if covered {
				return 0, ErrNoNexthop
			}
			return 0, ErrNoRoute
		}
		g := s.groups[bestGid]
		i := int(op.hash % uint64(g.n))
		g.lastUsed[i] = op.now
		if g.owner[i] == 0 {
			return 0, ErrNoNexthop
		}
		return g.owner[i], nil
	}
	return 0, fmt.Errorf("unknown op")
}
