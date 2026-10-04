package activate

import (
	"fmt"

	"ontology/guard"
	"ontology/roster"
)

type mState int

const (
	mReg mState = iota
	mAct
	mReset
)

type mRec struct {
	known   bool
	tenant  Tenant
	until   int64
	state   mState
	id, gen int64
	fp      Fp
}

type mGuard struct {
	e         int
	k         int
	lockUntil int64
}

type model struct {
	m, n   int
	lk     int64
	now    int64
	quota  map[Tenant]int
	used   map[Tenant]int
	recs   map[Sn]*mRec
	gd     map[Sn]*mGuard
	nextID int64
}

func newModel(m, n int, lk int64) *model {
	return &model{
		m: m, n: n, lk: lk,
		quota: map[Tenant]int{}, used: map[Tenant]int{},
		recs: map[Sn]*mRec{}, gd: map[Sn]*mGuard{},
	}
}

func (mm *model) guard(sn Sn) *mGuard {
	g, ok := mm.gd[sn]
	if !ok {
		g = &mGuard{}
		mm.gd[sn] = g
	}
	return g
}

func mValid(s string, t int64) bool {
	return len(s) >= 1 && len(s) <= 64 && t >= 0 && t <= 1_000_000_000_000
}

func (mm *model) register(batch Batch, tenant Tenant, until int64, sns []Sn, now int64) (error, string) {
	if batch == "" || tenant == "" || now < 0 || now > 1e12 || until < 0 || until > 1e12 ||
		len(sns) < 1 || len(sns) > 10_000 {
		return ErrInvalid, "arg out of range"
	}
	for _, sn := range sns {
		if len(sn) < 1 || len(sn) > 64 {
			return ErrInvalid, "bad sn length"
		}
	}
	if now < mm.now {
		return ErrClockBack, "now<maxNow"
	}
	if _, ok := mm.quota[tenant]; !ok {
		return ErrInvalid, "unknown tenant"
	}
	seen := map[Sn]bool{}
	for _, sn := range sns {
		if seen[sn] {
			return ErrDupSn, "intra-batch dup"
		}
		seen[sn] = true
		if mm.recs[sn] != nil {
			return ErrDupSn, "already registered"
		}
	}
	for _, sn := range sns {
		mm.recs[sn] = &mRec{known: true, tenant: tenant, until: until, state: mReg}
	}
	mm.now = now
	return nil, "accepted"
}

func (mm *model) activate(sn Sn, fp Fp, now int64) (Result, error, string) {
	if len(sn) < 1 || len(sn) > 64 || len(fp) < 1 || len(fp) > 64 ||
		now < 0 || now > 1e12 {
		return Result{}, ErrInvalid, "arg out of range"
	}
	if now < mm.now {
		return Result{}, ErrClockBack, "now<maxNow"
	}
	r := mm.recs[sn]
	if r == nil {
		return Result{}, ErrUnknown, "no roster record"
	}
	g := mm.guard(sn)
	if now < g.lockUntil {
		return Result{}, ErrLocked, fmt.Sprintf("now<lockUntil=%d", g.lockUntil)
	}
	switch r.state {
	case mAct:
		if fp == r.fp {
			mm.now = now
			return Result{r.id, r.gen}, nil, "idempotent replay"
		}
		mm.now = now // ErrConflict 是唯一推进时钟的拒绝。
		g.e++
		reason := fmt.Sprintf("conflict e=%d/%d", g.e, mm.m)
		if g.e >= mm.m {
			g.e = 0
			g.k++
			exp := g.k - 1
			if exp > 6 {
				exp = 6
			}
			g.lockUntil = now + mm.lk<<exp
			reason = fmt.Sprintf("conflict lock k=%d until=%d", g.k, g.lockUntil)
		}
		return Result{}, ErrConflict, reason
	case mReg:
		if now >= r.until {
			return Result{}, ErrBatchClosed, fmt.Sprintf("now=%d>=until=%d", now, r.until)
		}
		if mm.used[r.tenant] >= mm.quota[r.tenant] {
			return Result{}, ErrQuota, "tenant quota full"
		}
		mm.now = now
		mm.used[r.tenant]++
		r.fp = fp
		r.state = mAct
		if r.id == 0 {
			mm.nextID++
			r.id = mm.nextID
		}
		r.gen++
		return Result{r.id, r.gen}, nil, "first bind, quota+1"
	case mReset:
		mm.now = now
		r.fp = fp
		r.state = mAct
		r.gen++
		return Result{r.id, r.gen}, nil, "rebind, no until/quota"
	}
	return Result{}, ErrState, "impossible"
}

func (mm *model) reset(sn Sn, now int64) (error, string) {
	if len(sn) < 1 || len(sn) > 64 || now < 0 || now > 1e12 {
		return ErrInvalid, "arg out of range"
	}
	if now < mm.now {
		return ErrClockBack, "now<maxNow"
	}
	r := mm.recs[sn]
	if r == nil {
		return ErrUnknown, "no roster record"
	}
	if r.state != mAct {
		return ErrState, "only Activated can reset"
	}
	mm.now = now
	r.state = mReset
	r.fp = ""
	g := mm.guard(sn)
	g.e = 0
	g.lockUntil = 0
	return nil, "reset: e=0, lock released, k kept"
}

func (mm *model) deactivate(sn Sn, now int64) (error, string) {
	if len(sn) < 1 || len(sn) > 64 || now < 0 || now > 1e12 {
		return ErrInvalid, "arg out of range"
	}
	if now < mm.now {
		return ErrClockBack, "now<maxNow"
	}
	r := mm.recs[sn]
	if r == nil {
		return ErrUnknown, "no roster record"
	}
	if r.state != mAct && r.state != mReset {
		return ErrState, "only Activated/ResetPending"
	}
	mm.now = now
	r.state = mReg
	r.fp = ""
	mm.used[r.tenant]--
	return nil, "quota released, id/gen kept"
}

var _ = mValid

func stateName(s roster.State) string {
	switch s {
	case roster.Registered:
		return "Registered"
	case roster.Activated:
		return "Activated"
	case roster.ResetPending:
		return "ResetPending"
	}
	return "?"
}

func svcStateLine(svc *Service, sn Sn) string {
	g := svc.gd.Peek(guard.Sn(sn))
	rec, err := svc.rs.Snapshot(sn)
	if err != nil {
		return fmt.Sprintf("guard{e:%d k:%d lu:%d} no-record", g.E, g.K, g.LockUntil)
	}
	return fmt.Sprintf("guard{e:%d k:%d lu:%d} %s id=%d gen=%d fp=%q usedT=%d",
		g.E, g.K, g.LockUntil, stateName(rec.State), rec.ID, rec.Gen, rec.Fp, svc.used[rec.Tenant])
}
