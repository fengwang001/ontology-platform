package access_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/access"
)

// opKind 枚举随机操作。
type opKind int

const (
	kSetOwners opKind = iota
	kTick
	kRequest
	kApprove
	kDeny
	kExtend
	kDelegate
	kRevoke
	kCheck
)

type rndOp struct {
	kind      opKind
	now       int64
	id        string
	u, r, who string
	to        string
	dur       int64
	extra     int64
	gid       int64
}

func genSeq(rng *rand.Rand) []rndOp {
	const usersN = 6
	const resN = 3
	user := func(i int) string { return fmt.Sprintf("u%d", i%usersN) }
	res := func(i int) string { return fmt.Sprintf("r%d", i%resN) }

	ops := []rndOp{{kind: kSetOwners, now: 0, r: "r0"}}
	for i := 1; i < resN; i++ {
		ops = append(ops, rndOp{kind: kSetOwners, now: 0, r: fmt.Sprintf("r%d", i)})
	}

	var base int64
	nextNow := func() int64 {
		if rng.Intn(8) == 0 {
			d := int64(rng.Intn(5))
			if base-d >= 0 {
				return base - d
			}
		}
		base += int64(rng.Intn(12))
		return base
	}

	reqSeq := 0
	grants := []int64{}
	n := 50 + rng.Intn(40)
	for len(ops) < n+3 {
		now := nextNow()
		switch rng.Intn(100) {
		case 0:
			ops = append(ops, rndOp{kind: kSetOwners, now: now, r: res(rng.Intn(resN))})
		case 1, 2:
			ops = append(ops, rndOp{kind: kTick, now: now})
		case 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14:
			reqSeq++
			id := fmt.Sprintf("q%d", reqSeq)
			dur := int64(1 + rng.Intn(60))
			if rng.Intn(15) == 0 {
				id = fmt.Sprintf("q%d", 1+rng.Intn(reqSeq+1))
			}
			if rng.Intn(20) == 0 {
				dur = int64(rng.Intn(2)) * 400
			}
			ops = append(ops, rndOp{kind: kRequest, now: now, id: id,
				u: user(rng.Intn(usersN)), r: res(rng.Intn(resN)), dur: dur})
		case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
			id := fmt.Sprintf("q%d", 1+rng.Intn(reqSeq+1))
			ops = append(ops, rndOp{kind: kApprove, now: now, id: id,
				who: []string{"o", user(rng.Intn(usersN))}[rng.Intn(2)],
				u:   user(rng.Intn(usersN)), r: res(rng.Intn(resN))})
		case 30, 31:
			id := fmt.Sprintf("q%d", 1+rng.Intn(reqSeq+1))
			ops = append(ops, rndOp{kind: kDeny, now: now, id: id,
				who: []string{"o", user(rng.Intn(usersN))}[rng.Intn(2)]})
		case 32, 33, 34:
			gid := int64(0)
			if len(grants) > 0 {
				gid = grants[rng.Intn(len(grants))]
			}
			ops = append(ops, rndOp{kind: kExtend, now: now, gid: gid,
				extra: int64(1 + rng.Intn(200))})
		case 35, 36, 37, 38, 39, 40, 41, 42, 43, 44:
			gid := int64(0)
			if len(grants) > 0 {
				gid = grants[rng.Intn(len(grants))]
			}
			ops = append(ops, rndOp{kind: kDelegate, now: now, gid: gid,
				to: user(rng.Intn(usersN)), dur: int64(1 + rng.Intn(120))})
		case 45, 46:
			gid := int64(0)
			if len(grants) > 0 {
				gid = grants[rng.Intn(len(grants))]
			}
			ops = append(ops, rndOp{kind: kRevoke, now: now, gid: gid,
				who: []string{"o", user(rng.Intn(usersN))}[rng.Intn(2)],
				r:   res(rng.Intn(resN))})
		default:
			ops = append(ops, rndOp{kind: kCheck, now: now,
				u: user(rng.Intn(usersN)), r: res(rng.Intn(resN))})
		}
	}

	// 用朴素模型先跑一遍，记录成功的授权编号（两模型编号同步，供生成器引用）。
	nm := newNaive(100, 2, 300, 2, 50)
	for i := range ops {
		o := &ops[i]
		switch o.kind {
		case kSetOwners:
			nm.setOwners(o.r, []string{"o"}, o.now)
		case kTick:
			nm.tick(o.now)
		case kRequest:
			nm.request(o.id, o.u, o.r, o.dur, o.now)
		case kApprove:
			_, gid := nm.approve(o.id, o.who, o.now)
			if gid > 0 {
				grants = append(grants, gid)
			}
		case kDeny:
			nm.deny(o.id, o.who, o.now)
		case kExtend:
			nm.extend(o.gid, o.extra, o.now)
		case kDelegate:
			_, gid := nm.delegate(o.gid, o.to, o.dur, o.now)
			if gid > 0 {
				grants = append(grants, gid)
			}
		case kRevoke:
			nm.revoke(o.gid, o.who, o.now)
		case kCheck:
			nm.check(o.u, o.r, o.now)
		}
	}
	return ops
}

func runReal(s *access.System, op rndOp) (string, int64, bool) {
	switch op.kind {
	case kSetOwners:
		return errName(s.SetOwners(b(op.r), [][]byte{b("o")}, op.now)), 0, false
	case kTick:
		return errName(s.Tick(op.now)), 0, false
	case kRequest:
		return errName(s.Request(op.id, b(op.u), b(op.r), op.dur, op.now)), 0, false
	case kApprove:
		gid, err := s.Approve(op.id, b(op.who), op.now)
		return errName(err), gid, false
	case kDeny:
		return errName(s.Deny(op.id, b(op.who), op.now)), 0, false
	case kExtend:
		return errName(s.Extend(op.gid, op.extra, op.now)), 0, false
	case kDelegate:
		gid, err := s.Delegate(op.gid, b(op.to), op.dur, op.now)
		return errName(err), gid, false
	case kRevoke:
		return errName(s.Revoke(op.gid, b(op.who), op.now)), 0, false
	case kCheck:
		return "nil", 0, s.Check(b(op.u), b(op.r), op.now)
	}
	return "", 0, false
}

func runNaive(nm *naiveModel, op rndOp) (string, int64, bool) {
	switch op.kind {
	case kSetOwners:
		return nm.setOwners(op.r, []string{"o"}, op.now), 0, false
	case kTick:
		return nm.tick(op.now), 0, false
	case kRequest:
		return nm.request(op.id, op.u, op.r, op.dur, op.now), 0, false
	case kApprove:
		name, gid := nm.approve(op.id, op.who, op.now)
		return name, gid, false
	case kDeny:
		return nm.deny(op.id, op.who, op.now), 0, false
	case kExtend:
		return nm.extend(op.gid, op.extra, op.now), 0, false
	case kDelegate:
		name, gid := nm.delegate(op.gid, op.to, op.dur, op.now)
		return name, gid, false
	case kRevoke:
		return nm.revoke(op.gid, op.who, op.now), 0, false
	case kCheck:
		return "nil", 0, nm.check(op.u, op.r, op.now)
	}
	return "", 0, false
}

func TestDifferential1500(t *testing.T) {
	const seqs = 1500
	for seed := int64(0); seed < seqs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genSeq(rng)
		s := access.New(access.Config{P: 100, M: 2, Lmax: 300, E: 2, Cool: 50})
		nm := newNaive(100, 2, 300, 2, 50)

		for i, op := range ops {
			rName, rGid, rChk := runReal(s, op)
			nName, nGid, nChk := runNaive(nm, op)
			t.Logf("seed=%d op=%d in=%+v out=(%s,g%d,check=%v) basis=(%s,g%d,check=%v)",
				seed, i, op, rName, rGid, rChk, nName, nGid, nChk)
			if rName != nName || rGid != nGid || rChk != nChk {
				t.Fatalf("seed=%d op=%d %+v: real=(%s,%d,%v) naive=(%s,%d,%v)",
					seed, i, op, rName, rGid, rChk, nName, nGid, nChk)
			}
			rl := s.Reaped()
			nl := nm.logSig()
			if len(rl) != len(nl) {
				t.Fatalf("seed=%d op=%d log len real=%d naive=%d", seed, i, len(rl), len(nl))
			}
			for k := range rl {
				if rl[k].GrantID != nl[k].id || string(rl[k].Cause) != nl[k].cause || rl[k].At != nl[k].at {
					t.Fatalf("seed=%d op=%d log[%d] real=(g%d,%s,%d) naive=(g%d,%s,%d)",
						seed, i, k, rl[k].GrantID, rl[k].Cause, rl[k].At,
						nl[k].id, nl[k].cause, nl[k].at)
				}
			}
		}
	}
}

// touched 与全局授权总数无关：100 与 10000 两档均 ≤ 3。
func TestTouchedScaling(t *testing.T) {
	for _, total := range []int{100, 10000} {
		s := access.New(access.Config{P: 1_000_000_000, M: total + 1, Lmax: 300, E: 1, Cool: 1})
		must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
		var target int64
		for i := 0; i < total; i++ {
			uid := fmt.Sprintf("u%d", i)
			rid := fmt.Sprintf("r%d", i)
			must(t, s.SetOwners(b(rid), [][]byte{b("o")}, 0))
			qid := fmt.Sprintf("q%d", i)
			must(t, s.Request(qid, b(uid), b(rid), 100, 0))
			gid, err := s.Approve(qid, b("o"), 0)
			must2(t, gid, err)
			if i == total-1 {
				target = gid
				cid, err := s.Delegate(gid, b("vh"), 50, 0)
				must2(t, cid, err)
			}
		}
		if !s.Check(b("vh"), b(fmt.Sprintf("r%d", total-1)), 10) {
			t.Fatalf("total=%d check false", total)
		}
		if got := s.Touched(); got > 3 {
			t.Fatalf("total=%d touched=%d > 3", total, got)
		}
		n := s.Grant(target)
		if n == nil {
			t.Fatalf("total=%d target grant missing", total)
		}
	}
}
