// Package fib 在下一跳组之上提供最长前缀路由与全局下一跳存活管理。
package fib

import (
	"errors"
	"sort"
	"sync"

	"ontology/bucket"
	"ontology/nhgroup"
)

// 拒绝分类（按报告优先级排列）。
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRewind     = errors.New("clock rewind")
	ErrNotFound        = errors.New("not found")
	ErrExists          = errors.New("already exists")
	ErrLastMember      = errors.New("last member")
	ErrInUse           = errors.New("group in use")
	ErrNoRoute         = errors.New("no route")
	ErrNoNexthop       = errors.New("no nexthop")
)

// Prefix 是一条 IPv4 前缀。
type Prefix struct {
	Addr uint32
	Len  uint8
}

// groupEntry 是 FIB 内部的一个组及其惰性迁移状态。
type groupEntry struct {
	g      *nhgroup.Group
	tbl    *bucket.Table
	ti, tu int64
	unbal  bool
	since  int64
}

// FIB 是转发信息库。
type FIB struct {
	mu     sync.RWMutex
	maxNow int64
	groups map[int]*groupEntry
	routes map[Prefix]int
	users  map[int]map[int]bool // nh -> set(gid)
	last   int                  // 最近一次 Lookup 触碰桶数（测试用）
	rec    int                  // 本操作整理阶段触碰桶数之和
}

// New 创建空 FIB。
func New() *FIB {
	return &FIB{
		groups: map[int]*groupEntry{},
		routes: map[Prefix]int{},
		users:  map[int]map[int]bool{},
	}
}

const maxID = 1_000_000

func validTime(now int64) bool { return now >= 0 && now <= 1e12 }

// CreateGroup 建组并立即完成整表指派。
func (f *FIB) CreateGroup(gid, n, ti, tu int, members []nhgroup.Spec, now int64) error {
	if gid < 1 || gid > maxID || n < 1 || n > 4096 || ti < 0 || ti > 1e9 ||
		tu < 0 || tu > 1e9 || len(members) < 1 || len(members) > 64 || !validTime(now) {
		return ErrInvalidArgument
	}
	seen := map[int]bool{}
	for _, m := range members {
		if m.NH < 1 || m.NH > maxID || m.Weight < 1 || m.Weight > 1000 || seen[m.NH] {
			return ErrInvalidArgument
		}
		seen[m.NH] = true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	if _, ok := f.groups[gid]; ok {
		return ErrExists
	}
	f.maxNow = now
	g := nhgroup.New(members)
	ge := &groupEntry{g: g, tbl: bucket.New(n), ti: int64(ti), tu: int64(tu)}
	ge.tbl.AssignAll(g.Targets(n), now)
	f.groups[gid] = ge
	for nh := range seen {
		f.track(nh, gid)
	}
	return nil
}

func (f *FIB) track(nh, gid int) {
	if f.users[nh] == nil {
		f.users[nh] = map[int]bool{}
	}
	f.users[nh][gid] = true
}

func (f *FIB) untrack(nh, gid int) {
	if set := f.users[nh]; set != nil {
		delete(set, gid)
		if len(set) == 0 {
			delete(f.users, nh)
		}
	}
}

func (f *FIB) userGroups(nh int) []int {
	out := make([]int, 0, len(f.users[nh]))
	for gid := range f.users[nh] {
		out = append(out, gid)
	}
	sort.Ints(out)
	return out
}

func (f *FIB) sortedGIDs() []int {
	out := make([]int, 0, len(f.groups))
	for gid := range f.groups {
		out = append(out, gid)
	}
	sort.Ints(out)
	return out
}

// GroupBuckets 返回组桶表快照（owner 列表），供测试与模拟复核。
func (f *FIB) GroupBuckets(gid int) ([]int, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ge, ok := f.groups[gid]
	if !ok {
		return nil, false
	}
	es := ge.tbl.Snapshot()
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = e.Owner
	}
	return out, true
}

// GroupLastUsed 返回组各桶 lastUsed 快照。
func (f *FIB) GroupLastUsed(gid int) ([]int64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ge, ok := f.groups[gid]
	if !ok {
		return nil, false
	}
	es := ge.tbl.Snapshot()
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.LastUsed
	}
	return out, true
}

// GroupUnbalancedSince 返回组是否不平衡及其 since（测试/模拟用）。
func (f *FIB) GroupUnbalancedSince(gid int) (int64, bool, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ge, ok := f.groups[gid]
	if !ok {
		return 0, false, false
	}
	return ge.since, ge.unbal, true
}

// Alive 报告某组成员存活状态（测试用）。
func (f *FIB) Alive(gid, nh int) (bool, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ge, ok := f.groups[gid]
	if !ok || !ge.g.Has(nh) {
		return false, false
	}
	return ge.g.Alive(nh), true
}

// reconcileAll 在每个被接受操作的开头整理所有不平衡的组。
func (f *FIB) reconcileAll(now int64) {
	f.rec = 0
	for _, gid := range f.sortedGIDs() {
		ge := f.groups[gid]
		if !ge.unbal {
			continue
		}
		ge.tbl.ResetTouched()
		targets := ge.g.Targets(ge.tbl.N())
		if ge.tbl.Reconcile(targets, now, ge.ti, ge.since, ge.tu) {
			ge.unbal = false
		}
		f.rec += ge.tbl.Touched()
	}
}

// afterChange 重判平衡，负责 unbalancedSince 的设置/清除。
func (ge *groupEntry) afterChange(now int64) {
	targets := ge.g.Targets(ge.tbl.N())
	if ge.tbl.Balanced(targets) {
		ge.unbal = false
		return
	}
	if !ge.unbal {
		ge.unbal = true
		ge.since = now
	}
}

// AddMember 加入成员；存活取全局状态，变更当次不搬桶。
func (f *FIB) AddMember(gid, nh, weight int, now int64) error {
	if gid < 1 || gid > maxID || nh < 1 || nh > maxID || weight < 1 || weight > 1000 ||
		!validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	ge, ok := f.groups[gid]
	if !ok {
		return ErrNotFound
	}
	if ge.g.Has(nh) {
		return ErrExists
	}
	f.maxNow = now
	f.reconcileAll(now)
	ge.g.Add(nh, weight, true)
	f.track(nh, gid)
	if idx := emptyIndices(ge); len(idx) > 0 {
		ge.tbl.Assign(idx, ge.g.Targets(ge.tbl.N()))
	}
	ge.afterChange(now)
	return nil
}

// isAlive 报告全局存活：任一引用组内失效即失效，未被引用按存活。
func (f *FIB) isAlive(nh int) bool {
	for _, gid := range f.userGroups(nh) {
		if ge := f.groups[gid]; ge != nil && ge.g.Has(nh) && !ge.g.Alive(nh) {
			return false
		}
	}
	return true
}

// SetWeight 修改成员权重；超配部分靠惰性迁移。
func (f *FIB) SetWeight(gid, nh, weight int, now int64) error {
	if gid < 1 || gid > maxID || nh < 1 || nh > maxID || weight < 1 || weight > 1000 ||
		!validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	ge, ok := f.groups[gid]
	if !ok || !ge.g.Has(nh) {
		return ErrNotFound
	}
	f.maxNow = now
	f.reconcileAll(now)
	ge.g.SetWeight(nh, weight)
	ge.afterChange(now)
	return nil
}

func bucketIndices(ge *groupEntry, nh int) []int {
	idx := make([]int, 0)
	for i, b := range ge.tbl.Snapshot() {
		if b.Owner == nh {
			idx = append(idx, i)
		}
	}
	return idx
}

func emptyIndices(ge *groupEntry) []int {
	idx := make([]int, 0)
	for i, b := range ge.tbl.Snapshot() {
		if b.Owner == 0 {
			idx = append(idx, i)
		}
	}
	return idx
}

// RemoveMember 删除成员并立即指派其名下桶；组内唯一成员报最后成员。
func (f *FIB) RemoveMember(gid, nh int, now int64) error {
	if gid < 1 || gid > maxID || nh < 1 || nh > maxID || !validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	ge, ok := f.groups[gid]
	if !ok || !ge.g.Has(nh) {
		return ErrNotFound
	}
	if len(ge.g.Members()) == 1 {
		return ErrLastMember
	}
	f.maxNow = now
	f.reconcileAll(now)
	idx := bucketIndices(ge, nh)
	ge.g.Remove(nh)
	f.untrack(nh, gid)
	if len(idx) > 0 {
		ge.tbl.Assign(idx, ge.g.Targets(ge.tbl.N()))
	}
	ge.afterChange(now)
	return nil
}

// NexthopDown 使下一跳全局失效：各组内其名下桶立即指派，无存活成员则置空。
func (f *FIB) NexthopDown(nh int, now int64) error {
	if nh < 1 || nh > maxID || !validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	gids := f.userGroups(nh)
	if len(gids) == 0 {
		return ErrNotFound
	}
	f.maxNow = now
	f.reconcileAll(now)
	for _, gid := range gids {
		ge := f.groups[gid]
		if !ge.g.Has(nh) || !ge.g.Alive(nh) {
			continue
		}
		idx := bucketIndices(ge, nh)
		ge.g.SetAlive(nh, false)
		if len(idx) > 0 {
			ge.tbl.Assign(idx, ge.g.Targets(ge.tbl.N()))
		}
		ge.afterChange(now)
	}
	return nil
}

// NexthopUp 恢复下一跳：空桶立即指派，非空桶靠惰性迁移。
func (f *FIB) NexthopUp(nh int, now int64) error {
	if nh < 1 || nh > maxID || !validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	gids := f.userGroups(nh)
	if len(gids) == 0 {
		return ErrNotFound
	}
	f.maxNow = now
	f.reconcileAll(now)
	for _, gid := range gids {
		f.nexthopRecover(f.groups[gid], nh, now)
	}
	return nil
}

// Lookup 在整理之后返回桶成员并更新 lastUsed。
func (f *FIB) Lookup(gid int, hash uint64, now int64) (int, error) {
	if gid < 1 || gid > maxID || !validTime(now) {
		return 0, ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return 0, ErrClockRewind
	}
	ge, ok := f.groups[gid]
	if !ok {
		return 0, ErrNotFound
	}
	f.maxNow = now
	f.reconcileAll(now)
	ge.tbl.ResetTouched()
	nh, found := ge.tbl.Lookup(hash, now)
	f.last = f.rec + ge.tbl.Touched()
	if !found {
		return 0, ErrNoNexthop
	}
	return nh, nil
}

// LastTouched 暴露最近一次 Lookup 触碰的桶数（测试用）。
func (f *FIB) LastTouched() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.last
}

func hostBitsOK(p Prefix) bool {
	if p.Len > 32 {
		return false
	}
	if p.Len == 0 {
		return p.Addr == 0
	}
	mask := ^uint32(0) << (32 - p.Len)
	return p.Addr&^mask == 0
}

// AddRoute 添加前缀到组的路由；同前缀同组重复添加为空操作，指向不同组报已存在。
func (f *FIB) AddRoute(p Prefix, gid int, now int64) error {
	if gid < 1 || gid > maxID || !validTime(now) || !hostBitsOK(p) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	if _, ok := f.groups[gid]; !ok {
		return ErrNotFound
	}
	if old, ok := f.routes[p]; ok && old != gid {
		return ErrExists
	}
	f.maxNow = now
	f.reconcileAll(now)
	f.routes[p] = gid
	return nil
}

// DelRoute 删除前缀路由。
func (f *FIB) DelRoute(p Prefix, now int64) error {
	if !validTime(now) || !hostBitsOK(p) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	if _, ok := f.routes[p]; !ok {
		return ErrNotFound
	}
	f.maxNow = now
	f.reconcileAll(now)
	delete(f.routes, p)
	return nil
}

// Route 取覆盖 dst 的最长前缀（全死组回退次长），在选中组上 Lookup。
func (f *FIB) Route(dst uint32, hash uint64, now int64) (int, error) {
	if !validTime(now) {
		return 0, ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return 0, ErrClockRewind
	}
	f.maxNow = now
	f.reconcileAll(now)
	var best Prefix
	bestLen := -1
	matched := false
	for p, gid := range f.routes {
		var mask uint32
		if p.Len > 0 {
			mask = ^uint32(0) << (32 - p.Len)
		}
		if dst&mask != p.Addr {
			continue
		}
		if int(p.Len) <= bestLen || len(f.groups[gid].g.AliveMembers()) == 0 {
			continue
		}
		best, bestLen, matched = p, int(p.Len), true
	}
	if !matched {
		if f.anyCovering(dst) {
			return 0, ErrNoNexthop
		}
		return 0, ErrNoRoute
	}
	ge := f.groups[f.routes[best]]
	ge.tbl.ResetTouched()
	nh, found := ge.tbl.Lookup(hash, now)
	f.last = f.rec + ge.tbl.Touched()
	if !found {
		return 0, ErrNoNexthop
	}
	return nh, nil
}

func (f *FIB) anyCovering(dst uint32) bool {
	for p := range f.routes {
		var mask uint32
		if p.Len > 0 {
			mask = ^uint32(0) << (32 - p.Len)
		}
		if dst&mask == p.Addr {
			return true
		}
	}
	return false
}

// DeleteGroup 删除组；仍被路由引用时报使用中。
func (f *FIB) DeleteGroup(gid int, now int64) error {
	if gid < 1 || gid > maxID || !validTime(now) {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.maxNow {
		return ErrClockRewind
	}
	ge, ok := f.groups[gid]
	if !ok {
		return ErrNotFound
	}
	for _, rgid := range f.routes {
		if rgid == gid {
			return ErrInUse
		}
	}
	f.maxNow = now
	f.reconcileAll(now)
	for _, nh := range ge.g.Members() {
		wasDead := !ge.g.Alive(nh)
		f.untrack(nh, gid)
		if wasDead {
			aliveElsewhere := true
			for _, ogid := range f.userGroups(nh) {
				og := f.groups[ogid]
				if og.g.Has(nh) && !og.g.Alive(nh) {
					aliveElsewhere = false
				}
			}
			if aliveElsewhere {
				for _, ogid := range f.userGroups(nh) {
					f.nexthopRecover(f.groups[ogid], nh, now)
				}
			}
		}
	}
	delete(f.groups, gid)
	return nil
}

// nexthopRecover 在单个组内恢复成员：空桶立即指派，其余靠惰性。
func (f *FIB) nexthopRecover(ge *groupEntry, nh int, now int64) {
	if !ge.g.Has(nh) || ge.g.Alive(nh) {
		return
	}
	ge.g.SetAlive(nh, true)
	if idx := emptyIndices(ge); len(idx) > 0 {
		ge.tbl.Assign(idx, ge.g.Targets(ge.tbl.N()))
	}
	ge.afterChange(now)
}
