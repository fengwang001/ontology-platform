// Package naive 是白板服务的独立朴素参考实现：
// 用切片保存自底向上全序，所有次序操作都通过线性扫描/切片搬移完成，
// 刻意与 whiteboard 的隐式 treap 数据结构不同，作为差分测试的预言机。
package naive

import (
	"fmt"
	"sync"
)

const (
	MaxGroupMembers = 200
	MinGroupMembers = 2
	MaxNow          = int64(1_000_000_000_000)
	MinTTL          = int64(1)
	MaxTTL          = int64(3600)
)

// Kind 与 whiteboard.RejectKind 数值与语义一致，便于差分比较。
type Kind int

const (
	KindInvalidArgument Kind = iota + 1
	KindClockBackward
	KindNotFound
	KindIllegal
	KindLocked
	KindConflict
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid-argument"
	case KindClockBackward:
		return "clock-backward"
	case KindNotFound:
		return "not-found"
	case KindIllegal:
		return "illegal-operation"
	case KindLocked:
		return "locked"
	case KindConflict:
		return "version-conflict"
	default:
		return fmt.Sprintf("reject-%d", int(k))
	}
}

// Error 是朴素模型错误：Holder/Remain 仅 Kind==KindLocked 时有效。
type Error struct {
	Kind     Kind
	Msg      string
	Holder   string
	ExpireAt int64
	Remain   int64
	TargetID string
}

func (e *Error) Error() string {
	if e.Kind == KindLocked {
		return fmt.Sprintf("locked: id=%q holder=%q remain=%ds", e.TargetID, e.Holder, e.Remain)
	}
	return e.Kind.String() + ": " + e.Msg
}

type Side int

const (
	Below Side = -1
	Above Side = 1
)

type elem struct {
	group   string
	lastRev int64
}

type lockRec struct {
	holder   string
	expireAt int64
}

// Board 是朴素实现的白板。
type Board struct {
	mu     sync.Mutex
	order  []string // 自底向上
	elems  map[string]*elem
	groups map[string]map[string]struct{}
	locks  map[string]lockRec
	rev    int64
	lastTs int64
}

// New 返回空白板。
func New() *Board {
	return &Board{
		elems:  map[string]*elem{},
		groups: map[string]map[string]struct{}{},
		locks:  map[string]lockRec{},
	}
}

// Snapshot 与 whiteboard.Snapshot 字段一致。
type Snapshot struct {
	Order  []string
	Rev    int64
	LastTs int64
	Elems  map[string]ElemInfo
	Groups map[string][]string
	Locks  map[string]LockState
}

type ElemInfo struct {
	Group   string
	LastRev int64
}

type LockState struct {
	TargetID string
	Holder   string
	ExpireAt int64
}

func fail(kind Kind, format string, args ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

func validTime(now int64) bool { return now >= 0 && now <= MaxNow }

func (n *Board) effectiveLock(id string, now int64) *lockRec {
	if l, ok := n.locks[id]; ok && l.expireAt > now {
		return &l
	}
	return nil
}

func (n *Board) purgeExpired(id string, now int64) {
	if l, ok := n.locks[id]; ok && l.expireAt <= now {
		delete(n.locks, id)
	}
}

func (n *Board) touchedLock(elemID, user string, now int64) *Error {
	if l := n.effectiveLock(elemID, now); l != nil && l.holder != user {
		return &Error{Kind: KindLocked, Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: elemID}
	}
	if g := n.elems[elemID].group; g != "" {
		if l := n.effectiveLock(g, now); l != nil && l.holder != user {
			return &Error{Kind: KindLocked, Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: g}
		}
	}
	return nil
}

func (n *Board) Add(user, id string, now int64) error {
	if user == "" || id == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	if _, ok := n.elems[id]; ok {
		return fail(KindIllegal, "exists element")
	}
	if _, ok := n.groups[id]; ok {
		return fail(KindIllegal, "exists group")
	}
	n.rev++
	n.order = append(n.order, id)
	n.elems[id] = &elem{lastRev: n.rev}
	n.lastTs = now
	return nil
}

func (n *Board) Group(user, groupID string, ids []string, now int64) error {
	if user == "" || groupID == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	if len(ids) < MinGroupMembers || len(ids) > MaxGroupMembers {
		return fail(KindInvalidArgument, "count")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return fail(KindInvalidArgument, "empty member")
		}
		if seen[id] {
			return fail(KindInvalidArgument, "dup")
		}
		seen[id] = true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	if _, ok := n.groups[groupID]; ok {
		return fail(KindIllegal, "group exists")
	}
	if _, ok := n.elems[groupID]; ok {
		return fail(KindIllegal, "collision")
	}
	for _, id := range ids {
		if _, ok := n.elems[id]; !ok {
			return fail(KindNotFound, "member missing")
		}
	}
	for _, id := range ids {
		if n.elems[id].group != "" {
			return fail(KindIllegal, "already grouped")
		}
	}
	for _, id := range ids {
		if e := n.touchedLock(id, user, now); e != nil {
			return e
		}
	}
	members := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		members[id] = struct{}{}
	}
	n.groups[groupID] = members
	n.rev++
	for _, id := range ids {
		n.elems[id].group = groupID
		n.elems[id].lastRev = n.rev
	}
	n.lastTs = now
	return nil
}

func (n *Board) Ungroup(user, groupID string, now int64) error {
	if user == "" || groupID == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	members, ok := n.groups[groupID]
	if !ok {
		if _, isElem := n.elems[groupID]; isElem {
			return fail(KindIllegal, "not a group")
		}
		return fail(KindNotFound, "no group")
	}
	for id := range members {
		if e := n.touchedLock(id, user, now); e != nil {
			return e
		}
	}
	n.rev++
	for id := range members {
		n.elems[id].group = ""
		n.elems[id].lastRev = n.rev
	}
	delete(n.groups, groupID)
	n.lastTs = now
	return nil
}

func (n *Board) Remove(user, id string, now int64) error {
	if user == "" || id == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	var victims []string
	if members, ok := n.groups[id]; ok {
		for m := range members {
			victims = append(victims, m)
		}
	} else if _, ok := n.elems[id]; ok {
		victims = []string{id}
	} else {
		return fail(KindNotFound, "missing")
	}
	if len(victims) == 1 && n.elems[id].group != "" {
		return fail(KindIllegal, "member remove")
	}
	victimSet := map[string]bool{}
	for _, v := range victims {
		victimSet[v] = true
		if e := n.touchedLock(v, user, now); e != nil {
			return e
		}
	}
	n.rev++
	kept := make([]string, 0, len(n.order))
	for _, x := range n.order {
		if !victimSet[x] {
			kept = append(kept, x)
		}
	}
	n.order = kept
	for v := range victimSet {
		delete(n.elems, v)
		delete(n.locks, v)
	}
	if _, isGroup := n.groups[id]; isGroup {
		delete(n.groups, id)
		delete(n.locks, id)
	}
	n.lastTs = now
	return nil
}

func (n *Board) Reorder(user, target, anchor string, side Side, baseRev, now int64) error {
	if user == "" || target == "" || anchor == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	if side != Above && side != Below {
		return fail(KindInvalidArgument, "side")
	}
	if baseRev < 0 {
		return fail(KindInvalidArgument, "baseRev")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	if _, ok := n.elems[anchor]; !ok {
		return fail(KindNotFound, "anchor")
	}
	var moving []string
	if members, ok := n.groups[target]; ok {
		for m := range members {
			moving = append(moving, m)
		}
	} else if e, ok := n.elems[target]; ok {
		if e.group != "" {
			return fail(KindIllegal, "member reorder")
		}
		moving = []string{target}
	} else {
		return fail(KindNotFound, "target")
	}
	movingSet := map[string]bool{}
	for _, m := range moving {
		movingSet[m] = true
	}
	if movingSet[anchor] {
		return fail(KindIllegal, "anchor in set")
	}
	for _, m := range moving {
		if e := n.touchedLock(m, user, now); e != nil {
			return e
		}
	}
	for _, m := range moving {
		if n.elems[m].lastRev > baseRev {
			return fail(KindConflict, "stale")
		}
	}
	segment := make([]string, 0, len(moving))
	for _, x := range n.order {
		if movingSet[x] {
			segment = append(segment, x)
		}
	}
	rest := make([]string, 0, len(n.order)-len(segment))
	anchorIdxAfter := -1
	for _, x := range n.order {
		if !movingSet[x] {
			if x == anchor {
				anchorIdxAfter = len(rest)
			}
			rest = append(rest, x)
		}
	}
	var next []string
	if side == Below {
		next = append(next, rest[:anchorIdxAfter]...)
		next = append(next, segment...)
		next = append(next, rest[anchorIdxAfter:]...)
	} else {
		next = append(next, rest[:anchorIdxAfter+1]...)
		next = append(next, segment...)
		next = append(next, rest[anchorIdxAfter+1:]...)
	}
	n.order = next
	n.rev++
	for _, m := range moving {
		n.elems[m].lastRev = n.rev
	}
	n.lastTs = now
	return nil
}

func (n *Board) Lock(user, id string, ttl, now int64) error {
	if user == "" || id == "" || ttl < MinTTL || ttl > MaxTTL || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	if _, e := n.elems[id]; !e {
		if _, g := n.groups[id]; !g {
			return fail(KindNotFound, "missing")
		}
	}
	n.purgeExpired(id, now)
	if l, ok := n.locks[id]; ok && l.holder != user {
		return &Error{Kind: KindLocked, Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: id}
	}
	n.locks[id] = lockRec{holder: user, expireAt: now + ttl}
	n.lastTs = now
	return nil
}

func (n *Board) Unlock(user, id string, now int64) error {
	if user == "" || id == "" || !validTime(now) {
		return fail(KindInvalidArgument, "bad args")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now < n.lastTs {
		return fail(KindClockBackward, "clock")
	}
	if _, e := n.elems[id]; !e {
		if _, g := n.groups[id]; !g {
			return fail(KindNotFound, "missing")
		}
	}
	n.purgeExpired(id, now)
	l, ok := n.locks[id]
	if !ok {
		n.lastTs = now
		return nil
	}
	if l.holder != user {
		return &Error{Kind: KindLocked, Holder: l.holder, ExpireAt: l.expireAt, Remain: l.expireAt - now, TargetID: id}
	}
	delete(n.locks, id)
	n.lastTs = now
	return nil
}

func (n *Board) Order() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.order...)
}

func (n *Board) Rank(id string) (int, error) {
	if id == "" {
		return 0, fail(KindInvalidArgument, "empty")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.elems[id]; !ok {
		if _, g := n.groups[id]; g {
			return 0, fail(KindIllegal, "group")
		}
		return 0, fail(KindNotFound, "missing")
	}
	return n.indexOfLocked(id) + 1, nil
}

func (n *Board) indexOfLocked(id string) int {
	for i, x := range n.order {
		if x == id {
			return i
		}
	}
	return -1
}

func (n *Board) Between(lo, hi int) ([]string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	total := len(n.order)
	if lo < 1 || hi < lo || hi > total {
		return nil, fail(KindInvalidArgument, "bounds [%d,%d] total=%d", lo, hi, total)
	}
	return append([]string(nil), n.order[lo-1:hi]...), nil
}

func (n *Board) Rev() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rev
}

func (n *Board) Snapshot(now int64) Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	snap := Snapshot{
		Order:  append([]string(nil), n.order...),
		Rev:    n.rev,
		LastTs: n.lastTs,
		Elems:  map[string]ElemInfo{},
		Groups: map[string][]string{},
		Locks:  map[string]LockState{},
	}
	for id, e := range n.elems {
		snap.Elems[id] = ElemInfo{Group: e.group, LastRev: e.lastRev}
	}
	for gid, members := range n.groups {
		var list []string
		for m := range members {
			list = append(list, m)
		}
		sortStrings(list)
		snap.Groups[gid] = list
	}
	for id, l := range n.locks {
		if l.expireAt <= now {
			continue
		}
		snap.Locks[id] = LockState{TargetID: id, Holder: l.holder, ExpireAt: l.expireAt}
	}
	return snap
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
