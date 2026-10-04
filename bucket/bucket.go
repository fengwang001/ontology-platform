package bucket

import (
	"sort"
	"sync"

	"ontology/nhgroup"
)

const maxNexthop = 1_000_000

type MemberSpec = nhgroup.Member

type memberState struct {
	weight uint32
	alive  bool
}

type bucketEntry struct {
	owner    uint32
	lastUsed uint64
}

type Group struct {
	id              uint32
	bucketCount     int
	idleThreshold   uint64
	unbalanceLimit  uint64
	members         map[uint32]*memberState
	buckets         []bucketEntry
	unbalancedSince uint64
	unbalanced      bool
	touched         int
}

type Controller struct {
	mu     sync.Mutex
	now    uint64
	groups map[uint32]*Group
	inUse  func(uint32) bool
}

func NewController() *Controller {
	return &Controller{groups: make(map[uint32]*Group)}
}

func (c *Controller) SetInUseHook(hook func(uint32) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inUse = hook
}

func (c *Controller) Lock() { c.mu.Lock() }

func (c *Controller) Unlock() { c.mu.Unlock() }

func (c *Controller) CheckTime(now uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkTimeLocked(now)
}

func (c *Controller) checkTimeLocked(now uint64) error {
	if now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	if now < c.now {
		return nhgroup.ErrClockRollback
	}
	return nil
}

func (c *Controller) GroupExistsLocked(gid uint32) bool {
	_, ok := c.groups[gid]
	return ok
}

func (c *Controller) groupForChange(gid uint32) (*Group, error) {
	group, ok := c.groups[gid]
	if !ok {
		return nil, nhgroup.ErrNotFound
	}
	return group, nil
}

func validMemberSpec(members []MemberSpec) error {
	if len(members) < 1 || len(members) > 64 {
		return nhgroup.ErrInvalidArgument
	}
	seen := make(map[uint32]bool, len(members))
	for _, member := range members {
		if member.Nexthop == 0 || member.Nexthop > maxNexthop || member.Weight == 0 || member.Weight > 1000 || !member.Alive {
			return nhgroup.ErrInvalidArgument
		}
		if seen[member.Nexthop] {
			return nhgroup.ErrInvalidArgument
		}
		seen[member.Nexthop] = true
	}
	return nil
}

func (c *Controller) CreateGroup(gid uint32, bucketCount, idleThreshold, unbalanceLimit uint64, members []MemberSpec, now uint64) error {
	if bucketCount < 1 || bucketCount > 4096 || idleThreshold > 1_000_000_000 || unbalanceLimit > 1_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	if err := validMemberSpec(members); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	if _, ok := c.groups[gid]; ok {
		return nhgroup.ErrAlreadyExists
	}
	c.prepareLocked(now)
	group := &Group{
		id:              gid,
		bucketCount:     int(bucketCount),
		idleThreshold:   idleThreshold,
		unbalanceLimit:  unbalanceLimit,
		members:         make(map[uint32]*memberState, len(members)),
		buckets:         make([]bucketEntry, bucketCount),
		unbalancedSince: now,
	}
	for _, member := range members {
		group.members[member.Nexthop] = &memberState{weight: member.Weight, alive: true}
	}
	targets := group.targets()
	deficits := targetDeficits(group, targets)
	for idx := range group.buckets {
		assignBucket(group, idx, deficits, now, true)
	}
	group.refreshBalance(targets, now)
	c.groups[gid] = group
	c.now = now
	return nil
}

func (c *Controller) DeleteGroup(gid uint32, now uint64) error {
	if now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	if _, ok := c.groups[gid]; !ok {
		return nhgroup.ErrNotFound
	}
	if c.inUse != nil && c.inUse(gid) {
		return nhgroup.ErrInUse
	}
	c.prepareLocked(now)
	delete(c.groups, gid)
	c.now = now
	return nil
}

func (c *Controller) AddMember(gid, nexthop uint32, weight uint32, now uint64) error {
	if nexthop == 0 || nexthop > maxNexthop || weight == 0 || weight > 1000 || now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	group, err := c.groupForChange(gid)
	if err != nil {
		return err
	}
	if _, ok := group.members[nexthop]; ok {
		return nhgroup.ErrAlreadyExists
	}
	c.prepareLocked(now)
	group.members[nexthop] = &memberState{weight: weight, alive: true}
	targets := group.targets()
	assignEmpty(group, targets)
	group.refreshBalance(targets, now)
	c.now = now
	return nil
}

func (c *Controller) SetWeight(gid, nexthop uint32, weight uint32, now uint64) error {
	if nexthop == 0 || nexthop > maxNexthop || weight == 0 || weight > 1000 || now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	group, err := c.groupForChange(gid)
	if err != nil {
		return err
	}
	member, ok := group.members[nexthop]
	if !ok {
		return nhgroup.ErrNotFound
	}
	c.prepareLocked(now)
	member.weight = weight
	targets := group.targets()
	assignEmpty(group, targets)
	group.refreshBalance(targets, now)
	c.now = now
	return nil
}

func (c *Controller) RemoveMember(gid, nexthop uint32, now uint64) error {
	if nexthop == 0 || nexthop > maxNexthop || now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	group, err := c.groupForChange(gid)
	if err != nil {
		return err
	}
	if _, ok := group.members[nexthop]; !ok {
		return nhgroup.ErrNotFound
	}
	if len(group.members) == 1 {
		return nhgroup.ErrLastMember
	}
	c.prepareLocked(now)
	delete(group.members, nexthop)
	targets := group.targets()
	deficits := targetDeficits(group, targets)
	for idx := range group.buckets {
		if group.buckets[idx].owner == nexthop {
			assignBucket(group, idx, deficits, now, false)
		}
	}
	group.refreshBalance(targets, now)
	c.now = now
	return nil
}

func (c *Controller) NexthopDown(nexthop uint32, now uint64) error {
	if nexthop == 0 || nexthop > maxNexthop || now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	if !c.nexthopExistsLocked(nexthop) {
		return nhgroup.ErrNotFound
	}
	c.prepareLocked(now)
	for _, group := range c.sortedGroupsLocked() {
		member, ok := group.members[nexthop]
		if !ok || !member.alive {
			continue
		}
		member.alive = false
		targets := group.targets()
		deficits := targetDeficits(group, targets)
		for idx := range group.buckets {
			if group.buckets[idx].owner == nexthop {
				assignBucket(group, idx, deficits, now, false)
			}
		}
		group.refreshBalance(targets, now)
	}
	c.now = now
	return nil
}

func (c *Controller) NexthopUp(nexthop uint32, now uint64) error {
	if nexthop == 0 || nexthop > maxNexthop || now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	if !c.nexthopExistsLocked(nexthop) {
		return nhgroup.ErrNotFound
	}
	c.prepareLocked(now)
	for _, group := range c.sortedGroupsLocked() {
		member, ok := group.members[nexthop]
		if !ok || member.alive {
			continue
		}
		member.alive = true
		targets := group.targets()
		assignEmpty(group, targets)
		group.refreshBalance(targets, now)
	}
	c.now = now
	return nil
}

func (c *Controller) nexthopExistsLocked(nexthop uint32) bool {
	for _, group := range c.groups {
		if _, ok := group.members[nexthop]; ok {
			return true
		}
	}
	return false
}

func (c *Controller) sortedGroupsLocked() []*Group {
	groups := make([]*Group, 0, len(c.groups))
	for _, group := range c.groups {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].id < groups[j].id })
	return groups
}

func (c *Controller) Lookup(gid uint32, hash, now uint64) (uint32, error) {
	if now > 1_000_000_000_000 {
		return 0, nhgroup.ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkTimeLocked(now); err != nil {
		return 0, err
	}
	group, err := c.groupForChange(gid)
	if err != nil {
		return 0, err
	}
	c.prepareLocked(now)
	return c.lookupPreparedLocked(group, hash, now)
}

func (c *Controller) PrepareLocked(now uint64) error {
	if err := c.checkTimeLocked(now); err != nil {
		return err
	}
	c.prepareLocked(now)
	c.now = now
	return nil
}

func (c *Controller) LookupPreparedLocked(gid uint32, hash, now uint64) (uint32, error) {
	group, ok := c.groups[gid]
	if !ok {
		return 0, nhgroup.ErrNotFound
	}
	return c.lookupPreparedLocked(group, hash, now)
}

func (c *Controller) GroupAliveLocked(gid uint32) bool {
	group, ok := c.groups[gid]
	if !ok {
		return false
	}
	for _, member := range group.members {
		if member.alive {
			return true
		}
	}
	return false
}

func (c *Controller) lookupPreparedLocked(group *Group, hash, now uint64) (uint32, error) {
	idx := int(hash % uint64(group.bucketCount))
	owner := group.buckets[idx].owner
	if owner == 0 {
		c.now = now
		return 0, nhgroup.ErrNoNexthop
	}
	group.buckets[idx].lastUsed = now
	group.touched++
	c.now = now
	return owner, nil
}

func (c *Controller) prepareLocked(now uint64) {
	for _, group := range c.sortedGroupsLocked() {
		group.touched = 0
		targets := group.targets()
		current := group.counts()
		if nhgroup.Balance(group.bucketCount, current, targets, aliveCount(group)) {
			group.unbalanced = false
			group.unbalancedSince = 0
			continue
		}
		group.rebalance(targets, now)
	}
}

func aliveCount(group *Group) int {
	count := 0
	for _, member := range group.members {
		if member.alive {
			count++
		}
	}
	return count
}

func (g *Group) targets() nhgroup.Targets {
	members := make([]nhgroup.Member, 0, len(g.members))
	for nexthop, member := range g.members {
		members = append(members, nhgroup.Member{Nexthop: nexthop, Weight: member.weight, Alive: member.alive})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Nexthop < members[j].Nexthop })
	return nhgroup.TargetBuckets(g.bucketCount, members)
}

func (g *Group) counts() map[uint32]int {
	counts := make(map[uint32]int, len(g.members))
	for _, entry := range g.buckets {
		if entry.owner != 0 {
			counts[entry.owner]++
		}
	}
	return counts
}

func targetDeficits(g *Group, targets nhgroup.Targets) map[uint32]int {
	counts := g.counts()
	deficits := make(map[uint32]int)
	for nexthop := range targets.Alive {
		deficit := targets.Buckets[nexthop] - counts[nexthop]
		if deficit > 0 {
			deficits[nexthop] = deficit
		}
	}
	return deficits
}

func chooseDeficit(deficits map[uint32]int) uint32 {
	best := uint32(0)
	bestDeficit := 0
	for nexthop, deficit := range deficits {
		if deficit <= 0 {
			continue
		}
		if deficit > bestDeficit || (deficit == bestDeficit && (best == 0 || nexthop < best)) {
			best = nexthop
			bestDeficit = deficit
		}
	}
	return best
}

func assignBucket(g *Group, idx int, deficits map[uint32]int, now uint64, initialize bool) {
	next := chooseDeficit(deficits)
	g.buckets[idx].owner = next
	if initialize {
		g.buckets[idx].lastUsed = now
	}
	if next != 0 {
		deficits[next]--
		if deficits[next] == 0 {
			delete(deficits, next)
		}
	}
}

func assignEmpty(g *Group, targets nhgroup.Targets) {
	deficits := targetDeficits(g, targets)
	for idx := range g.buckets {
		if g.buckets[idx].owner == 0 {
			assignBucket(g, idx, deficits, 0, false)
		}
	}
}

func (g *Group) refreshBalance(targets nhgroup.Targets, now uint64) {
	balanced := nhgroup.Balance(g.bucketCount, g.counts(), targets, aliveCount(g))
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

func (g *Group) rebalance(targets nhgroup.Targets, now uint64) {
	forced := g.unbalanceLimit > 0 && now-g.unbalancedSince >= g.unbalanceLimit
	surplus := make(map[uint32]int)
	for nexthop := range targets.Alive {
		if extra := g.counts()[nexthop] - targets.Buckets[nexthop]; extra > 0 {
			surplus[nexthop] = extra
		}
	}
	deficits := targetDeficits(g, targets)
	for idx := range g.buckets {
		owner := g.buckets[idx].owner
		if owner == 0 || surplus[owner] <= 0 {
			continue
		}
		idle := now-g.buckets[idx].lastUsed >= g.idleThreshold
		if !idle && !forced {
			continue
		}
		next := chooseDeficit(deficits)
		if next == 0 {
			continue
		}
		g.buckets[idx].owner = next
		surplus[owner]--
		if surplus[owner] == 0 {
			delete(surplus, owner)
		}
		deficits[next]--
		if deficits[next] == 0 {
			delete(deficits, next)
		}
		g.touched++
	}
	g.refreshBalance(targets, now)
}
