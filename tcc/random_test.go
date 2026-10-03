package tcc

import (
	"errors"
	"math/rand"
	"testing"
)

type op struct {
	kind          int // 0 Try 1 Confirm 2 Cancel
	xid, br, acct string
	amt, now      int64
}

func TestRandomDifferential(t *testing.T) {
	for _, seed := range []int64{1, 42, 99, 2026} {
		diffRun(t, seed)
	}
}

func diffRun(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	const accts = 4
	dep := map[string]int64{}
	for i := 0; i < accts; i++ {
		dep[string(rune('a'+i))] = 80
	}
	for iter := 0; iter < 200; iter++ {
		rm, lg, _ := newEnv(t, 8, 30, dep)
		nm := newNaive(8, 30, dep)
		var seq []op
		now := int64(0)
		for step := 0; step < 120; step++ {
			xid := string(rune('x' + rng.Intn(3)))
			br := string(rune('1' + rng.Intn(4)))
			acct := string(rune('a' + rng.Intn(accts)))
			amt := int64(1 + rng.Intn(100))
			if rng.Intn(5) == 0 {
				now += int64(rng.Intn(12)) // 有时跳到到期之后
			}
			o := op{kind: rng.Intn(3), xid: xid, br: br, acct: acct, amt: amt, now: now}
			seq = append(seq, o)
			var got, want error
			switch o.kind {
			case 0:
				got = rm.Try(o.xid, o.br, o.acct, o.amt, o.now)
				want = nm.try(o.xid, o.br, o.acct, o.amt, o.now)
			case 1:
				got = rm.Confirm(o.xid, o.br, o.now)
				want = nm.confirm(o.xid, o.br, o.now)
			default:
				got = rm.Cancel(o.xid, o.br, o.now)
				want = nm.cancel(o.xid, o.br, o.now)
			}
			if !sameErr(got, want) {
				t.Fatalf("iter %d step %d op=%+v\ngot=%v want=%v", iter, step, o, got, want)
			}
		}
		// 对照每个账户的 bal/fz 与每条记录。
		for i := 0; i < accts; i++ {
			a := string(rune('a' + i))
			if lg.Bal(a) != nm.bal[a] || lg.Fz(a) != nm.fz[a] {
				t.Fatalf("iter %d ledger %s: got bal/fz %d/%d want %d/%d",
					iter, a, lg.Bal(a), lg.Fz(a), nm.bal[a], nm.fz[a])
			}
		}
		for x := 0; x < 3; x++ {
			for b := 0; b < 4; b++ {
				xid, br := string(rune('x'+x)), string(rune('1'+b))
				gb, gok := rm.Get(xid, br)
				wb, wok := nm.state[key(xid, br)]
				if gok != wok {
					t.Fatalf("iter %d rec %s/%s presence got=%v want=%v", iter, xid, br, gok, wok)
				}
				if gok && (gb.State != wb.st || gb.Acct != wb.acct ||
					gb.Amount != wb.amt || gb.Reason != wb.reason) {
					t.Fatalf("iter %d rec %s/%s got=%+v want=%+v", iter, xid, br, gb, wb)
				}
			}
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, target := range []error{
		ErrInvalid, ErrClock, ErrHanging, ErrMismatch, ErrState, ErrNoBranch,
		ErrExpired, ErrConflict, ErrCapacity, ErrInsufficient,
	} {
		if errors.Is(a, target) != errors.Is(b, target) {
			return false
		}
	}
	return true
}
