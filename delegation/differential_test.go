package delegation

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// 随机操作序列上，Service 与朴素参照实现逐项对照。
func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	clockA := NewManualClock(t0)
	clockB := NewManualClock(t0)
	svc := NewService(clockA, nil)
	naive := NewNaiveService(clockB)

	subjects := []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7"}
	perms := []Permission{
		{ObjectType: "Order", Attribute: "amount", RowScope: "region:cn"},
		{ObjectType: "Order", Attribute: "amount", RowScope: "*"},
		{ObjectType: "Order", Attribute: "*", RowScope: "*"},
		{ObjectType: "Customer", Attribute: "name", RowScope: "*"},
		{ObjectType: "Customer", Attribute: "ssn", RowScope: "dept:hr"},
		{ObjectType: "Product", Attribute: "price", RowScope: "*"},
	}
	randSubject := func() string { return subjects[rng.Intn(len(subjects))] }
	randPerm := func() Permission { return perms[rng.Intn(len(perms))] }
	randSubset := func() []Permission {
		n := 1 + rng.Intn(3)
		out := make([]Permission, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, randPerm())
		}
		return out
	}

	var declaredIDs []uint64
	now := func() time.Time { return clockA.Now() }

	for i := 0; i < 3000; i++ {
		// 每若干步推进时钟，制造过期与历史区间。
		if rng.Intn(4) == 0 {
			d := time.Duration(1+rng.Intn(30)) * time.Minute
			clockA.Advance(d)
			clockB.Advance(d)
		}
		switch op := rng.Intn(100); {
		case op < 20: // 授予直接权限
			subj, p := randSubject(), randPerm()
			svc.GrantDirect(subj, p)
			naive.GrantDirect(subj, p)
		case op < 35: // 收缩直接权限
			subj, p := randSubject(), randPerm()
			svc.RevokeDirect(subj, p)
			naive.RevokeDirect(subj, p)
		case op < 65: // 声明委托
			start := now().Add(time.Duration(rng.Intn(121)-60) * time.Minute)
			in := DelegationInput{
				Delegator:       randSubject(),
				Delegatee:       randSubject(),
				Subset:          randSubset(),
				AllowRedelegate: rng.Intn(2) == 0,
				ValidFrom:       start,
				ValidTo:         start.Add(time.Duration(1+rng.Intn(180)) * time.Minute),
			}
			idA, errA := svc.Declare(in)
			idB, errB := naive.Declare(in)
			if errA != errB {
				t.Fatalf("iter %d: Declare(%+v) err mismatch: svc=%v naive=%v", i, in, errA, errB)
			}
			if errA == nil {
				if idA != idB {
					t.Fatalf("iter %d: id mismatch: svc=%d naive=%d", i, idA, idB)
				}
				declaredIDs = append(declaredIDs, idA)
			}
		case op < 75: // 撤销委托
			if len(declaredIDs) == 0 {
				continue
			}
			id := declaredIDs[rng.Intn(len(declaredIDs))]
			errA := svc.Revoke(id)
			errB := naive.Revoke(id)
			if (errA == nil) != (errB == nil) {
				t.Fatalf("iter %d: Revoke(%d) err mismatch: svc=%v naive=%v", i, id, errA, errB)
			}
		case op < 90: // 当前判定
			subj, p := randSubject(), randPerm()
			gotA := svc.Check(subj, p).Allowed
			gotB := naive.Check(subj, p)
			if gotA != gotB {
				t.Fatalf("iter %d: Check(%s,%v) mismatch: svc=%v naive=%v", i, subj, p, gotA, gotB)
			}
		default: // 历史时刻判定
			subj, p := randSubject(), randPerm()
			asOf := t0.Add(time.Duration(rng.Int63n(int64(now().Sub(t0)) + 1)))
			gotA := svc.CheckAt(subj, p, asOf).Allowed
			gotB := naive.CheckAt(subj, p, asOf)
			if gotA != gotB {
				t.Fatalf("iter %d: CheckAt(%s,%v,%s) mismatch: svc=%v naive=%v", i, subj, p, asOf, gotA, gotB)
			}
		}
	}
}

// 并发调用等价于某个串行顺序：把并发期记录的日志（按 seq 即串行化顺序）
// 在朴素实现上重放，输出必须逐项一致。
func TestConcurrentLinearizable(t *testing.T) {
	clock := NewManualClock(t0)
	rec := NewMemoryRecorder()
	svc := NewService(clock, rec)

	subjects := []string{"s0", "s1", "s2", "s3"}
	perms := []Permission{
		{ObjectType: "Order", Attribute: "amount", RowScope: "*"},
		{ObjectType: "Customer", Attribute: "name", RowScope: "*"},
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(worker) + 1))
			for i := 0; i < 200; i++ {
				subj := subjects[rng.Intn(len(subjects))]
				p := perms[rng.Intn(len(perms))]
				switch rng.Intn(6) {
				case 0:
					svc.GrantDirect(subj, p)
				case 1:
					svc.RevokeDirect(subj, p)
				case 2:
					from := clock.Now()
					svc.Declare(DelegationInput{
						Delegator:       subj,
						Delegatee:       subjects[rng.Intn(len(subjects))],
						Subset:          []Permission{p},
						AllowRedelegate: rng.Intn(2) == 0,
						ValidFrom:       from,
						ValidTo:         from.Add(time.Hour),
					})
				case 3:
					svc.Revoke(uint64(1 + rng.Intn(64)))
				case 4:
					svc.Check(subj, p)
				default:
					svc.CheckAt(subj, p, clock.Now())
				}
			}
		}(w)
	}
	wg.Wait()

	// 按日志顺序（即串行化顺序）在朴素实现上重放。
	replayClock := NewManualClock(t0)
	naive := NewNaiveService(replayClock)
	for _, e := range rec.Entries() {
		replayClock.Set(e.Time)
		switch e.Op {
		case OpGrantDirect:
			naive.GrantDirect(e.Subject, e.Permissions...)
		case OpRevokeDirect:
			naive.RevokeDirect(e.Subject, e.Permissions...)
		case OpDeclare:
			id, err := naive.Declare(DelegationInput{
				Delegator:       e.Delegator,
				Delegatee:       e.Delegatee,
				Subset:          e.Permissions,
				AllowRedelegate: e.AllowReleg,
				ValidFrom:       e.ValidFrom,
				ValidTo:         e.ValidTo,
			})
			if err != e.Err {
				t.Fatalf("replay declare seq=%d: err %v != recorded %v", e.Seq, err, e.Err)
			}
			if err == nil && id != e.DelegationID {
				t.Fatalf("replay declare seq=%d: id %d != recorded %d", e.Seq, id, e.DelegationID)
			}
		case OpRevoke:
			if err := naive.Revoke(e.DelegationID); (err == nil) != (e.Err == nil) {
				t.Fatalf("replay revoke seq=%d: err %v != recorded %v", e.Seq, err, e.Err)
			}
		case OpCheck:
			if got := naive.Check(e.Subject, e.Permissions[0]); got != e.Allowed {
				t.Fatalf("replay check seq=%d: %v != recorded %v", e.Seq, got, e.Allowed)
			}
		case OpCheckAt:
			if got := naive.CheckAt(e.Subject, e.Permissions[0], e.AsOf); got != e.Allowed {
				t.Fatalf("replay checkAt seq=%d: %v != recorded %v", e.Seq, got, e.Allowed)
			}
		}
	}
}

// 可观测证明：判定遍历的委托记录数只与实际支持路径长度相关，
// 与系统中累计存在的委托记录总数无关。
func TestTraversalBoundIndependentOfTotalRecords(t *testing.T) {
	clock := NewManualClock(t0)
	svc := NewService(clock, nil)
	from, to := clock.Now(), clock.Now().Add(24*time.Hour)

	// 目标委托链：c0 → c1 → … → c8（长度 8）。
	const chainLen = 8
	svc.GrantDirect("c0", permP)
	for i := 0; i < chainLen; i++ {
		if _, err := svc.Declare(DelegationInput{
			Delegator:       fmt.Sprintf("c%d", i),
			Delegatee:       fmt.Sprintf("c%d", i+1),
			Subset:          []Permission{permP},
			AllowRedelegate: true,
			ValidFrom:       from, ValidTo: to,
		}); err != nil {
			t.Fatalf("chain declare: %v", err)
		}
	}
	target := fmt.Sprintf("c%d", chainLen)

	baseline := svc.Check(target, permP)
	if !baseline.Allowed {
		t.Fatalf("chain should allow")
	}
	// 上界：遍历量不超过链长的常数倍（有效/可委托两次遍历）。
	if baseline.NodesVisited > 4*chainLen {
		t.Fatalf("NodesVisited = %d, want <= %d", baseline.NodesVisited, 4*chainLen)
	}

	// 灌入大量与目标无关的委托记录（总数持续增长）。
	for batch := 0; batch < 2; batch++ {
		for i := 0; i < 1000; i++ {
			name := fmt.Sprintf("noise-%d-%d", batch, i)
			svc.GrantDirect(name, permR)
			if _, err := svc.Declare(DelegationInput{
				Delegator: name,
				Delegatee: name + "-peer",
				Subset:    []Permission{permR},
				ValidFrom: from, ValidTo: to,
			}); err != nil {
				t.Fatalf("noise declare: %v", err)
			}
		}
		after := svc.Check(target, permP)
		if !after.Allowed {
			t.Fatalf("chain should still allow")
		}
		if after.NodesVisited != baseline.NodesVisited {
			t.Fatalf("NodesVisited grew with total records: baseline=%d after batch %d=%d",
				baseline.NodesVisited, batch, after.NodesVisited)
		}
	}
}
