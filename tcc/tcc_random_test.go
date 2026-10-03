package tcc_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/ledger"
	"ontology/tcc"
)

// modelBranch 是朴素模拟的分支状态。
type modelBranch struct {
	acct   string
	amt    int64
	dl     int64
	state  tcc.State
	reason tcc.CancelReason
}

type naiveModel struct {
	bal   map[string]int64
	fz    map[string]int64
	brs   map[string]*modelBranch
	ttl   int64
	n     int
	clock int64
}

func newNaive(ttl int64, n int) *naiveModel {
	return &naiveModel{
		bal: map[string]int64{}, fz: map[string]int64{},
		brs: map[string]*modelBranch{}, ttl: ttl, n: n,
	}
}

// dueNow 返回 now（含恰等）到期但尚未落实的 Tried 分支（纯函数，不改状态）。
func (md *naiveModel) dueNow(now int64) []*modelBranch {
	var due []*modelBranch
	for _, b := range md.brs {
		if b.state == tcc.StateTried && now >= b.dl {
			due = append(due, b)
		}
	}
	return due
}

func (md *naiveModel) released(acct string, due []*modelBranch) int64 {
	var r int64
	for _, b := range due {
		if b.acct == acct {
			r += b.amt
		}
	}
	return r
}

func (md *naiveModel) applyDue(due []*modelBranch) {
	for _, b := range due {
		md.fz[b.acct] -= b.amt
		b.state = tcc.StateCancelled
		b.reason = tcc.CancelExpired
	}
}

// vState 返回目标分支在考虑虚拟到期后的状态与原因。
func vState(b *modelBranch, due []*modelBranch) (tcc.State, tcc.CancelReason) {
	if b != nil && b.state == tcc.StateTried {
		for _, d := range due {
			if d == b {
				return tcc.StateCancelled, tcc.CancelExpired
			}
		}
	}
	if b == nil {
		return tcc.StateTried, 0
	}
	return b.state, b.reason
}

func (md *naiveModel) call(op string, xid, br, acct string, amt, now int64) error {
	bad := len(xid) == 0 || len(br) == 0 || len(acct) == 0 || amt < 1 || amt > tcc.MaxAmount
	if now < 0 || now > tcc.MaxNow || bad {
		return ledger.ErrParam
	}
	if now < md.clock {
		return ledger.ErrClock
	}
	due := md.dueNow(now)
	key := xid + "/" + br
	b := md.brs[key]
	st, reason := vState(b, due)
	switch op {
	case "Try":
		switch {
		case b != nil && st == tcc.StateTried:
			if b.acct != acct || b.amt != amt {
				return ledger.ErrMismatch
			}
			md.applyDue(due)
			md.clock = now
			return nil
		case b != nil && st == tcc.StateConfirmed:
			return ledger.ErrState
		case b != nil: // 虚拟到期或既有取消均为悬挂
			return ledger.ErrHanging
		case len(md.brs) >= md.n:
			return ledger.ErrCapacity
		case md.bal[acct]-md.fz[acct]+md.released(acct, due) < amt:
			return ledger.ErrInsufficient
		}
		md.applyDue(due)
		md.fz[acct] += amt
		md.brs[key] = &modelBranch{acct: acct, amt: amt, dl: now + md.ttl, state: tcc.StateTried}
		md.clock = now
	case "Confirm":
		switch {
		case b == nil:
			return ledger.ErrNoBranch
		case st == tcc.StateConfirmed:
			md.applyDue(due)
			md.clock = now
		case st == tcc.StateCancelled && reason == tcc.CancelExpired:
			return ledger.ErrExpired
		case st == tcc.StateCancelled:
			return ledger.ErrConflict
		default:
			md.applyDue(due)
			md.fz[b.acct] -= b.amt
			md.bal[b.acct] -= b.amt
			b.state = tcc.StateConfirmed
			md.clock = now
		}
	case "Cancel":
		switch {
		case b == nil && len(md.brs) >= md.n:
			return ledger.ErrCapacity
		case b == nil:
			md.applyDue(due)
			md.brs[key] = &modelBranch{state: tcc.StateCancelled, reason: tcc.CancelEmpty}
			md.clock = now
		case st == tcc.StateCancelled:
			md.applyDue(due)
			md.clock = now
		case st == tcc.StateConfirmed:
			return ledger.ErrConflict
		default:
			md.applyDue(due)
			md.fz[b.acct] -= b.amt
			b.state = tcc.StateCancelled
			b.reason = tcc.CancelExplicit
			md.clock = now
		}
	}
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil)
}

// TestRandomAgainstNaive 将随机操作序列同时喂给实现与朴素模型，逐笔比对结果与账目。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 200; iter++ {
		ttl := int64(1 + rng.Intn(8))
		n := 1 + rng.Intn(5)
		lg := ledger.New()
		m, err := tcc.New(lg, ttl, n)
		if err != nil {
			t.Fatal(err)
		}
		md := newNaive(ttl, n)
		accts := []string{"a", "b", "c"}
		for _, ac := range accts {
			v := int64(1 + rng.Intn(40))
			if err := lg.Deposit([]byte(ac), v); err != nil {
				t.Fatal(err)
			}
			md.bal[ac] = v
		}
		xid := "X"
		for step := 0; step < 120; step++ {
			op := []string{"Try", "Confirm", "Cancel"}[rng.Intn(3)]
			br := fmt.Sprintf("%d", rng.Intn(4))
			ac := accts[rng.Intn(len(accts))]
			amt := int64(1 + rng.Intn(30))
			now := int64(rng.Intn(40))
			want := md.call(op, xid, br, ac, amt, now)
			var got error
			switch op {
			case "Try":
				got = m.Try([]byte(xid), []byte(br), []byte(ac), amt, now)
			case "Confirm":
				got = m.Confirm([]byte(xid), []byte(br), now)
			default:
				got = m.Cancel([]byte(xid), []byte(br), now)
			}
			t.Logf("iter=%d %s(%s,%s,%s,%d,%d) -> got=%v model=%v 判定:%s",
				iter, op, xid, br, ac, amt, now, got, want,
				map[bool]string{true: "一致", false: "不一致"}[sameErr(got, want)])
			if !sameErr(got, want) {
				t.Fatalf("iter=%d step=%d %s: got %v want %v", iter, step, op, got, want)
			}
			for _, ac := range accts {
				if lg.Balance([]byte(ac)) != md.bal[ac] || lg.Frozen([]byte(ac)) != md.fz[ac] {
					t.Fatalf("iter=%d 账目不匹配 acct=%s got(%d,%d) want(%d,%d)",
						iter, ac, lg.Balance([]byte(ac)), lg.Frozen([]byte(ac)),
						md.bal[ac], md.fz[ac])
				}
			}
			var fzSum, triedSum int64
			for _, ac := range accts {
				fzSum += lg.Frozen([]byte(ac))
			}
			for _, b := range md.brs {
				if b.state == tcc.StateTried {
					triedSum += b.amt
				}
			}
			if fzSum != triedSum {
				t.Fatalf("fzSum=%d != triedSum=%d", fzSum, triedSum)
			}
		}
	}
}
