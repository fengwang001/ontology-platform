// Package reaper 负责到期回收、级联失效与撤销冷却（无锁）。
package reaper

import (
	"ontology/grant"
	"strconv"
)

// Cause 是失效原因。
type Cause string

const (
	Expired  Cause = "Expired"
	Cascaded Cause = "Cascaded"
	Revoked  Cause = "Revoked"
)

// Entry 是一条回收日志。
type Entry struct {
	GrantID int64
	Cause   Cause
	At      int64
}

// Reaper 持有回收日志与冷却表。
type Reaper struct {
	log      []Entry
	cooldown map[string]int64
}

// New 创建空回收器。
func New() *Reaper { return &Reaper{cooldown: make(map[string]int64)} }

// Log 返回回收日志的拷贝。
func (rp *Reaper) Log() []Entry {
	out := make([]Entry, len(rp.log))
	copy(out, rp.log)
	return out
}

// Cooling 报告 (u,r) 在 now 是否仍处冷却（恰等解除）。
func (rp *Reaper) Cooling(u, r []byte, now, cool int64) bool {
	return now < rp.cooldown[key(u, r)]
}

// SetCooldown 登记直接撤销的冷却截止时刻。
func (rp *Reaper) SetCooldown(u, r []byte, until int64) { rp.cooldown[key(u, r)] = until }

// Sweep 处理 end ≤ now 的活动授权：按 (end,id) 逐个 Expired，
// 每个到期者仍活动的后代立即按先序（子 id 升序、孙紧随其子）记 Cascaded。
func (rp *Reaper) Sweep(t *grant.Tree, now int64) {
	for _, n := range t.Snapshot() {
		if !n.Alive() || n.End > now {
			continue
		}
		rp.expire(t, n, n.End)
	}
}

// Revoke 记录直接撤销并把仍活动的后代先序记为 Cascaded。
func (rp *Reaper) Revoke(t *grant.Tree, n *grant.Node, now int64) {
	if n == nil || !n.Alive() {
		return
	}
	rp.log = append(rp.log, Entry{GrantID: n.ID, Cause: Revoked, At: now})
	t.Invalidate(n)
	rp.cascade(t, n.ID, now)
}

// expire 记录 n 的失效（调用方给定原因时刻），随后把仍活动的后代
// 按先序（子按 id 升序、孙紧随其子）记 Cascaded。
func (rp *Reaper) expire(t *grant.Tree, n *grant.Node, at int64) {
	rp.log = append(rp.log, Entry{GrantID: n.ID, Cause: Expired, At: at})
	t.Invalidate(n)
	rp.cascade(t, n.ID, at)
}

func (rp *Reaper) cascade(t *grant.Tree, parentID int64, at int64) {
	for _, cid := range t.Children(parentID) {
		c := t.Get(cid)
		if !c.Alive() {
			continue
		}
		rp.log = append(rp.log, Entry{GrantID: cid, Cause: Cascaded, At: at})
		t.Invalidate(c)
		rp.cascade(t, cid, at)
	}
}

func key(u, r []byte) string {
	return strconv.Itoa(len(u)) + ":" + string(u) + "/" + strconv.Itoa(len(r)) + ":" + string(r)
}
