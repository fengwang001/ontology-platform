package bucket

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/nhgroup"
)

type simMember struct {
	weight uint32
	alive  bool
}

type simBucket struct {
	owner    uint32
	lastUsed uint64
}

type simGroup struct {
	id              uint32
	n               int
	ti              uint64
	tu              uint64
	members         map[uint32]simMember
	buckets         []simBucket
	unbalanced      bool
	unbalancedSince uint64
}

type simulator struct {
	now    uint64
	groups map[uint32]*simGroup
	log    []string
}

type simOp struct {
	name string
	gid  uint32
	nh   uint32
	w    uint32
	hash uint64
	now  uint64
	n    int
	step int
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			controller := NewController()
			sim := &simulator{groups: map[uint32]*simGroup{}}
			for step := 0; step < 40; step++ {
				op := randomOp(rng, sim)
				op.step = step
				wantNH, wantErr := sim.apply(op)
				gotNH, gotErr := controller.apply(op)
				if !sameError(gotErr, wantErr) || gotNH != wantNH {
					t.Fatalf("step %d op %+v got (%d,%v), want (%d,%v)\n%s", step, op, gotNH, gotErr, wantNH, wantErr, sim.journal())
				}
				if diverged(controller, sim) {
					t.Fatalf("state diverged after step %d op %+v\n%s", step, op, sim.journal())
				}
				assertSimInvariants(t, controller, sim)
			}
			t.Logf("seed %d: 40 operations checked; groups=%d; last=%s", seed, len(sim.groups), sim.log[len(sim.log)-1])
		})
	}
}

func randomOp(rng *rand.Rand, sim *simulator) simOp {
	if len(sim.groups) == 0 {
		return simOp{name: "create", gid: uint32(rng.Intn(3) + 1), now: sim.now, n: 1 + rng.Intn(16), nh: 1 + uint32(rng.Intn(4)), w: 1}
	}
	gids := make([]uint32, 0, len(sim.groups))
	for gid := range sim.groups {
		gids = append(gids, gid)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	gid := gids[rng.Intn(len(gids))]
	group := sim.groups[gid]
	names := []string{"lookup", "add", "weight", "remove", "down", "up", "create", "delete"}
	op := simOp{name: names[rng.Intn(len(names))], gid: gid, now: sim.now + uint64(rng.Intn(8)), n: 1 + rng.Intn(16)}
	op.nh = 1 + uint32(rng.Intn(8))
	op.w = 1 + uint32(rng.Intn(5))
	op.hash = uint64(rng.Intn(group.n))
	ids := make([]uint32, 0, len(group.members))
	for nh := range group.members {
		ids = append(ids, nh)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > 0 && rng.Intn(2) == 0 {
		op.nh = ids[rng.Intn(len(ids))]
	}
	return op
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.Error() == want.Error()
}

func createMembers(op simOp) []MemberSpec {
	count := 1 + int(op.nh%3)
	result := make([]MemberSpec, 0, count)
	used := map[uint32]bool{}
	for idx := 0; idx < count; idx++ {
		nh := 1 + uint32((int(op.nh)+idx)%6)
		if used[nh] {
			continue
		}
		used[nh] = true
		result = append(result, MemberSpec{Nexthop: nh, Weight: 1 + uint32((int(op.w)+idx)%4), Alive: true})
	}
	return result
}

func (c *Controller) apply(op simOp) (uint32, error) {
	switch op.name {
	case "create":
		return 0, c.CreateGroup(op.gid, uint64(op.n), 3+uint64(op.gid%4), 8, createMembers(op), op.now)
	case "lookup":
		return c.Lookup(op.gid, op.hash, op.now)
	case "add":
		return 0, c.AddMember(op.gid, op.nh, op.w, op.now)
	case "weight":
		return 0, c.SetWeight(op.gid, op.nh, op.w, op.now)
	case "remove":
		return 0, c.RemoveMember(op.gid, op.nh, op.now)
	case "down":
		return 0, c.NexthopDown(op.nh, op.now)
	case "up":
		return 0, c.NexthopUp(op.nh, op.now)
	case "delete":
		return 0, c.DeleteGroup(op.gid, op.now)
	}
	return 0, nhgroup.ErrInvalidArgument
}

func (s *simulator) reject(err error) (uint32, error) {
	s.log[len(s.log)-1] += fmt.Sprintf(" output=%v decision=rejected", err)
	return 0, err
}

func (s *simulator) accept(nh uint32) (uint32, error) {
	s.log[len(s.log)-1] += fmt.Sprintf(" output=%d decision=accepted compared=true", nh)
	return nh, nil
}

func (s *simulator) journal() string {
	start := len(s.log) - 20
	if start < 0 {
		start = 0
	}
	result := ""
	for _, line := range s.log[start:] {
		result += line + "\n"
	}
	return result
}

func (s *simulator) apply(op simOp) (uint32, error) {
	s.log = append(s.log, fmt.Sprintf("input=%+v", op))
	if op.now < s.now {
		return s.reject(nhgroup.ErrClockRollback)
	}
	switch op.name {
	case "create":
		memberSpecs := createMembers(op)
		if op.n < 1 || op.n > 4096 || len(memberSpecs) == 0 {
			return s.reject(nhgroup.ErrInvalidArgument)
		}
		if _, ok := s.groups[op.gid]; ok {
			return s.reject(nhgroup.ErrAlreadyExists)
		}
		s.housekeep(op.now)
		group := &simGroup{id: op.gid, n: op.n, ti: 3 + uint64(op.gid%4), tu: 8, members: map[uint32]simMember{}, buckets: make([]simBucket, op.n), unbalancedSince: op.now}
		for _, member := range memberSpecs {
			group.members[member.Nexthop] = simMember{weight: member.Weight, alive: true}
		}
		deficits := simDeficits(group, simTargets(group))
		for idx := range group.buckets {
			simAssign(group, idx, deficits, op.now, true)
		}
		group.refresh(simTargets(group), op.now)
		s.groups[op.gid] = group
		s.now = op.now
		return s.accept(0)
	case "lookup":
		group, ok := s.groups[op.gid]
		if !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		s.housekeep(op.now)
		idx := int(op.hash % uint64(group.n))
		if group.buckets[idx].owner == 0 {
			s.now = op.now
			return s.reject(nhgroup.ErrNoNexthop)
		}
		group.buckets[idx].lastUsed = op.now
		nh := group.buckets[idx].owner
		s.now = op.now
		return s.accept(nh)
	case "add":
		group, ok := s.groups[op.gid]
		if !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		if _, exists := group.members[op.nh]; exists {
			return s.reject(nhgroup.ErrAlreadyExists)
		}
		s.housekeep(op.now)
		group.members[op.nh] = simMember{weight: op.w, alive: true}
		simAssignEmpty(group, simTargets(group))
		group.refresh(simTargets(group), op.now)
		s.now = op.now
		return s.accept(0)
	case "weight":
		group, ok := s.groups[op.gid]
		if !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		member, ok := group.members[op.nh]
		if !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		s.housekeep(op.now)
		member.weight = op.w
		group.members[op.nh] = member
		simAssignEmpty(group, simTargets(group))
		group.refresh(simTargets(group), op.now)
		s.now = op.now
		return s.accept(0)
	case "remove":
		group, ok := s.groups[op.gid]
		if !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		if _, ok := group.members[op.nh]; !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		if len(group.members) == 1 {
			return s.reject(nhgroup.ErrLastMember)
		}
		s.housekeep(op.now)
		delete(group.members, op.nh)
		deficits := simDeficits(group, simTargets(group))
		for idx := range group.buckets {
			if group.buckets[idx].owner == op.nh {
				simAssign(group, idx, deficits, op.now, false)
			}
		}
		group.refresh(simTargets(group), op.now)
		s.now = op.now
		return s.accept(0)
	case "down", "up":
		if !simHasMember(s, op.nh) {
			return s.reject(nhgroup.ErrNotFound)
		}
		s.housekeep(op.now)
		for _, group := range simSortedGroups(s) {
			member, ok := group.members[op.nh]
			if !ok {
				continue
			}
			if (op.name == "down" && !member.alive) || (op.name == "up" && member.alive) {
				continue
			}
			member.alive = op.name == "up"
			group.members[op.nh] = member
			if !member.alive {
				deficits := simDeficits(group, simTargets(group))
				for idx := range group.buckets {
					if group.buckets[idx].owner == op.nh {
						simAssign(group, idx, deficits, op.now, false)
					}
				}
			} else {
				simAssignEmpty(group, simTargets(group))
			}
			group.refresh(simTargets(group), op.now)
		}
		s.now = op.now
		return s.accept(0)
	case "delete":
		if _, ok := s.groups[op.gid]; !ok {
			return s.reject(nhgroup.ErrNotFound)
		}
		s.housekeep(op.now)
		delete(s.groups, op.gid)
		s.now = op.now
		return s.accept(0)
	}
	return s.reject(nhgroup.ErrInvalidArgument)
}

func simSortedGroups(s *simulator) []*simGroup {
	result := make([]*simGroup, 0, len(s.groups))
	for _, group := range s.groups {
		result = append(result, group)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].id < result[j].id
	})
	return result
}

func simHasMember(s *simulator, nh uint32) bool {
	for _, group := range s.groups {
		if _, ok := group.members[nh]; ok {
			return true
		}
	}
	return false
}

func simTargets(group *simGroup) nhgroup.Targets {
	memberSpecs := make([]nhgroup.Member, 0, len(group.members))
	for nh, member := range group.members {
		memberSpecs = append(memberSpecs, nhgroup.Member{Nexthop: nh, Weight: member.weight, Alive: member.alive})
	}
	sort.Slice(memberSpecs, func(i, j int) bool { return memberSpecs[i].Nexthop < memberSpecs[j].Nexthop })
	return nhgroup.TargetBuckets(group.n, memberSpecs)
}

func simCounts(group *simGroup) map[uint32]int {
	counts := map[uint32]int{}
	for _, entry := range group.buckets {
		if entry.owner != 0 {
			counts[entry.owner]++
		}
	}
	return counts
}

func simDeficits(group *simGroup, targets nhgroup.Targets) map[uint32]int {
	counts := simCounts(group)
	deficits := map[uint32]int{}
	for nh := range targets.Alive {
		if deficit := targets.Buckets[nh] - counts[nh]; deficit > 0 {
			deficits[nh] = deficit
		}
	}
	return deficits
}

func simChoose(deficits map[uint32]int) uint32 {
	best := uint32(0)
	bestDeficit := 0
	for nh, deficit := range deficits {
		if deficit > bestDeficit || (deficit == bestDeficit && (best == 0 || nh < best)) {
			best = nh
			bestDeficit = deficit
		}
	}
	return best
}

func simAssign(group *simGroup, idx int, deficits map[uint32]int, now uint64, initialize bool) {
	next := simChoose(deficits)
	group.buckets[idx].owner = next
	if initialize {
		group.buckets[idx].lastUsed = now
	}
	if next != 0 {
		deficits[next]--
		if deficits[next] == 0 {
			delete(deficits, next)
		}
	}
}

func simAssignEmpty(group *simGroup, targets nhgroup.Targets) {
	deficits := simDeficits(group, targets)
	for idx := range group.buckets {
		if group.buckets[idx].owner == 0 {
			simAssign(group, idx, deficits, 0, false)
		}
	}
}

func (g *simGroup) refresh(targets nhgroup.Targets, now uint64) {
	balanced := nhgroup.Balance(g.n, simCounts(g), targets, simAliveCount(g))
	if balanced {
		g.unbalanced = false
		g.unbalancedSince = 0
		return
	}
	if !g.unbalanced {
		g.unbalanced = true
		g.unbalancedSince = now
	}
}

func simAliveCount(group *simGroup) int {
	count := 0
	for _, member := range group.members {
		if member.alive {
			count++
		}
	}
	return count
}

func (s *simulator) housekeep(now uint64) {
	for _, group := range simSortedGroups(s) {
		targets := simTargets(group)
		if nhgroup.Balance(group.n, simCounts(group), targets, simAliveCount(group)) {
			continue
		}
		forced := group.tu > 0 && now-group.unbalancedSince >= group.tu
		surplus := map[uint32]int{}
		counts := simCounts(group)
		for nh := range targets.Alive {
			if extra := counts[nh] - targets.Buckets[nh]; extra > 0 {
				surplus[nh] = extra
			}
		}
		deficits := simDeficits(group, targets)
		for idx := range group.buckets {
			owner := group.buckets[idx].owner
			if owner == 0 || surplus[owner] <= 0 {
				continue
			}
			if now-group.buckets[idx].lastUsed < group.ti && !forced {
				continue
			}
			next := simChoose(deficits)
			if next == 0 {
				continue
			}
			group.buckets[idx].owner = next
			surplus[owner]--
			if surplus[owner] == 0 {
				delete(surplus, owner)
			}
			deficits[next]--
			if deficits[next] == 0 {
				delete(deficits, next)
			}
		}
		group.refresh(targets, now)
	}
}

func diverged(controller *Controller, sim *simulator) bool {
	if len(controller.groups) != len(sim.groups) {
		return true
	}
	for gid, simGroup := range sim.groups {
		realGroup, ok := controller.groups[gid]
		if !ok || len(realGroup.buckets) != len(simGroup.buckets) {
			return true
		}
		for idx := range simGroup.buckets {
			if realGroup.buckets[idx].owner != simGroup.buckets[idx].owner ||
				realGroup.buckets[idx].lastUsed != simGroup.buckets[idx].lastUsed {
				return true
			}
		}
		if len(realGroup.members) != len(simGroup.members) {
			return true
		}
		for nh, simMember := range simGroup.members {
			realMember, ok := realGroup.members[nh]
			if !ok || realMember.weight != simMember.weight || realMember.alive != simMember.alive {
				return true
			}
		}
		if realGroup.unbalanced != simGroup.unbalanced ||
			(realGroup.unbalanced && realGroup.unbalancedSince != simGroup.unbalancedSince) {
			return true
		}
	}
	return false
}

func assertSimInvariants(t *testing.T, controller *Controller, sim *simulator) {
	t.Helper()
	for gid, group := range controller.groups {
		alive := 0
		for _, member := range group.members {
			if member.alive {
				alive++
			}
		}
		for idx, entry := range group.buckets {
			if entry.owner == 0 {
				if alive > 0 {
					t.Fatalf("group %d bucket %d empty with alive members", gid, idx)
				}
				continue
			}
			member, ok := group.members[entry.owner]
			if !ok || !member.alive {
				t.Fatalf("group %d bucket %d points at missing/dead member %d", gid, idx, entry.owner)
			}
		}
	}
}
