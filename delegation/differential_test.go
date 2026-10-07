package delegation

import (
	"errors"
	"math/rand"
	"testing"
	"time"
)

// TestDifferentialAgainstNaive 在大量随机生成的委托链与变更序列上，
// 将 Service 与独立维护的朴素参照实现 Naive 逐项对照：
// 每个操作的结果（含错误类型）与每次判定的结论都必须一致。
func TestDifferentialAgainstNaive(t *testing.T) {
	seeds := []int64{1, 7, 42, 1337, 20261007}
	for _, seed := range seeds {
		t.Run("", func(t *testing.T) {
			runDifferential(t, rand.New(rand.NewSource(seed)), 400)
		})
	}
}

func runDifferential(t *testing.T, rng *rand.Rand, steps int) {
	t.Helper()
	clk := NewManualClock(t0)
	svc := NewService(WithClock(clk))
	ref := NewNaive(clk)

	principals := []string{"p0", "p1", "p2", "p3", "p4", "p5"}
	objTypes := []string{"ot0", "ot1"}
	attrs := []string{"a0", "a1", "a2"}
	rows := []string{"r0", "r1"}

	randPerm := func() PermissionSet {
		out := PermissionSet{}
		ot := objTypes[rng.Intn(len(objTypes))]
		op := ObjectPerm{Attrs: map[string]bool{}, Rows: map[string]bool{}}
		for _, a := range attrs {
			if rng.Intn(2) == 0 {
				op.Attrs[a] = true
			}
		}
		for _, r := range rows {
			if rng.Intn(3) == 0 {
				op.Rows[r] = true
			}
		}
		out[ot] = op
		return out
	}

	// randSubsetOfEffective 从主体当前有效权限中随机取子集，
	// 用于提高"子集合法但超出可再委托范围"等窄窗口错误的命中率。
	randSubsetOfEffective := func(p string) PermissionSet {
		eff := svc.EffectivePermissions(p, clk.Now())
		out := PermissionSet{}
		for ot, op := range eff {
			n := ObjectPerm{Attrs: map[string]bool{}, Rows: map[string]bool{}}
			for a := range op.Attrs {
				if rng.Intn(2) == 0 {
					n.Attrs[a] = true
				}
			}
			for r := range op.Rows {
				if rng.Intn(2) == 0 {
					n.Rows[r] = true
				}
			}
			if !n.IsEmpty() {
				out[ot] = n
			}
		}
		if len(out) == 0 {
			return randPerm()
		}
		return out
	}

	var liveIDs []DelegationID
	var accepted []DelegationRequest
	errSeen := map[error]int{}

	// compareAll 在多个时刻对全部主体做判定对照。
	compareAll := func(step int) {
		t.Helper()
		now := clk.Now()
		times := []time.Time{now}
		// 随机取若干历史时刻，覆盖历史判定不可追溯改变的要求。
		for i := 0; i < 3; i++ {
			times = append(times, t0.Add(time.Duration(rng.Intn(3600*8))*time.Second))
		}
		for _, at := range times {
			for _, p := range principals {
				for _, ot := range objTypes {
					req := AccessRequest{
						Subject:    p,
						ObjectType: ot,
						Require: ObjectPerm{
							Attrs: map[string]bool{attrs[rng.Intn(len(attrs))]: true},
						},
						At: at,
					}
					got := svc.Decide(req)
					want := ref.Decide(req)
					if got.Allowed != want.Allowed {
						t.Fatalf("step %d: Decide(%+v) 分歧: service=%v naive=%v",
							step, req, got.Allowed, want.Allowed)
					}
				}
			}
		}
	}

	for step := 0; step < steps; step++ {
		switch rng.Intn(6) {
		case 0:
			p := principals[rng.Intn(len(principals))]
			perms := randPerm()
			svc.GrantBase(p, perms)
			ref.GrantBase(p, perms)
		case 1:
			p := principals[rng.Intn(len(principals))]
			perms := randPerm()
			svc.ShrinkBase(p, perms)
			ref.ShrinkBase(p, perms)
		case 2, 3:
			delegator := principals[rng.Intn(len(principals))]
			subset := randPerm()
			switch rng.Intn(4) {
			case 0:
				subset = randSubsetOfEffective(delegator)
			case 1:
				// 有针对性地以"当前生效且不允许再委托"的既有委托的
				// 受托方为委托方，提高"上游不允许再委托"窗口的命中率。
				var cands []DelegationRequest
				now := clk.Now()
				for _, prev := range accepted {
					if !prev.AllowRedelegate && !prev.Start.After(now) && prev.End.After(now) {
						cands = append(cands, prev)
					}
				}
				if len(cands) > 0 {
					prev := cands[rng.Intn(len(cands))]
					delegator = prev.Delegatee
					subset = prev.Subset
				}
			}
			req := DelegationRequest{
				Delegator: delegator,
				Delegatee: principals[rng.Intn(len(principals))],
				Subset:    subset,
				Start:     clk.Now().Add(time.Duration(rng.Intn(4)-1) * time.Hour),
				End:       clk.Now().Add(time.Duration(rng.Intn(8)) * time.Hour),
				// 约半数允许再委托。
				AllowRedelegate: rng.Intn(2) == 0,
			}
			gotID, gotErr := svc.Delegate(req)
			wantID, wantErr := ref.Delegate(req)
			if !errors.Is(gotErr, wantErr) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("step %d: Delegate(%+v) 错误分歧: service=%v naive=%v",
					step, req, gotErr, wantErr)
			}
			if gotErr == nil {
				if gotID != wantID {
					t.Fatalf("step %d: 委托 ID 分歧: service=%d naive=%d", step, gotID, wantID)
				}
				liveIDs = append(liveIDs, gotID)
			} else {
				errSeen[gotErr]++
			}
			if gotErr == nil {
				accepted = append(accepted, req)
			}
		case 4:
			if len(liveIDs) > 0 {
				id := liveIDs[rng.Intn(len(liveIDs))]
				gotErr := svc.Revoke(id)
				wantErr := ref.Revoke(id)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("step %d: Revoke(%d) 分歧: service=%v naive=%v",
						step, id, gotErr, wantErr)
				}
			}
		case 5:
			clk.Advance(time.Duration(1+rng.Intn(3)) * time.Hour)
		}
		if step%10 == 9 {
			compareAll(step)
		}
	}
	compareAll(steps)

	// 随机序列必须真实触发全部四类委托错误，否则覆盖不充分。
	for _, e := range []error{ErrSubsetExceeds, ErrRedelegateNotAllowed, ErrCycle, ErrExpired} {
		if errSeen[e] == 0 {
			t.Fatalf("随机序列未触发错误类型: %v", e)
		}
	}
}
