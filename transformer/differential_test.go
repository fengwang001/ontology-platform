package transformer

import (
	"fmt"
	"math/rand"
	"testing"
)

func errStr(e *Error) string {
	if e == nil {
		return "ok"
	}
	if e.Category == ErrCapacity {
		return fmt.Sprintf("%s(%s,t=%d)", e.Category, e.Level, e.Time)
	}
	return e.Category.String()
}

func sameErr(a, b *Error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Category == b.Category && a.Level == b.Level && a.Time == b.Time
}

func assertSameState(t *testing.T, seed int64, step int, sys *System, m *naiveModel) {
	t.Helper()
	snap := sys.Snapshot()
	if len(snap) != len(m.res) {
		t.Fatalf("seed=%d step=%d: 预约数 impl=%d model=%d", seed, step, len(snap), len(m.res))
	}
	if sys.Now() != m.now {
		t.Fatalf("seed=%d step=%d: 当前时刻 impl=%d model=%d", seed, step, sys.Now(), m.now)
	}
	for i, r := range snap {
		mr := m.res[i]
		if r.ID != mr.id || r.Status != mr.status || r.Start != mr.start ||
			r.End != mr.end || r.Power != mr.power || r.Feeder != mr.feeder ||
			r.CreatedAt != mr.created || r.HoldExpiresAt != mr.expiry {
			t.Fatalf("seed=%d step=%d: 预约 %d 状态不一致 impl=%+v model=%+v",
				seed, step, r.ID, r, mr)
		}
	}
}

// TestDifferentialRandom 用随机操作序列对拍实现与独立朴素模型，
// 逐条打印输入、输出与判定依据（go test -v 可见）。
func TestDifferentialRandom(t *testing.T) {
	limits := map[string]int{"f0": 6, "f1": 8, "f2": 5}
	feeders := []string{"f0", "f1", "f2", "ghost"}
	for seed := int64(1); seed <= 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sys := NewSystem(Config{HoldDuration: 4}, limits, 14)
		mdl := newNaiveModel(4, limits, 14)
		created := 0
		for step := 0; step < 300; step++ {
			now := sys.Now()
			var desc string
			var gotErr, wantErr *Error
			switch x := rng.Intn(100); {
			case x < 35:
				f := feeders[rng.Intn(len(feeders))]
				start := now - 2 + rng.Intn(25)
				end := start + rng.Intn(13) - 1
				power := rng.Intn(10)
				desc = fmt.Sprintf("create(%s,%d,%d,%d)", f, start, end, power)
				gotID, ge := sys.Create(f, start, end, power)
				wantID, we := mdl.create(f, start, end, power)
				gotErr, wantErr = ge, we
				if ge == nil {
					created++
					if gotID != wantID {
						t.Fatalf("seed=%d step=%d %s: ID impl=%d model=%d", seed, step, desc, gotID, wantID)
					}
				}
			case x < 50:
				id := 1 + rng.Intn(created+3)
				desc = fmt.Sprintf("confirm(%d)", id)
				gotErr, wantErr = sys.Confirm(id), mdl.confirm(id)
			case x < 60:
				id := 1 + rng.Intn(created+3)
				desc = fmt.Sprintf("release(%d)", id)
				gotErr, wantErr = sys.Release(id), mdl.release(id)
			case x < 75:
				id := 1 + rng.Intn(created+3)
				start := now - 2 + rng.Intn(25)
				end := start + rng.Intn(13) - 1
				power := rng.Intn(10)
				desc = fmt.Sprintf("modify(%d,%d,%d,%d)", id, start, end, power)
				gotErr, wantErr = sys.Modify(id, start, end, power), mdl.modify(id, start, end, power)
			case x < 88:
				eff := now - 1 + rng.Intn(25)
				cap := 3 + rng.Intn(18)
				desc = fmt.Sprintf("capacity(%d,%d)", eff, cap)
				gotErr, wantErr = sys.AddCapacityRecord(eff, cap), mdl.addCapacity(eff, cap)
			default:
				to := now + rng.Intn(7)
				if rng.Intn(10) == 0 {
					to = now - rng.Intn(3)
				}
				desc = fmt.Sprintf("advance(%d)", to)
				gotErr, wantErr = sys.AdvanceTime(to), mdl.advance(to)
			}
			t.Logf("seed=%d step=%03d now=%d %-26s impl=%-22s model=%s",
				seed, step, now, desc, errStr(gotErr), errStr(wantErr))
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("seed=%d step=%d %s: impl=%v model=%v", seed, step, desc, gotErr, wantErr)
			}
			assertSameState(t, seed, step, sys, mdl)
		}
		assertInvariants(t, sys, limits)
	}
}

// TestCheckCostIndependentOfLoad 验证两级校验开销不随不相交预约数量增长，
// 容量表查询为对数级（用计数器验证，而非墙钟时间）。
func TestCheckCostIndependentOfLoad(t *testing.T) {
	limits := map[string]int{"main": 1 << 30, "aux": 1 << 30}
	sys := NewSystem(Config{HoldDuration: 100000}, limits, 1<<30)
	measure := func() Stats {
		sys.ResetStats()
		if _, err := sys.Create("main", 50, 60, 1); err != nil {
			t.Fatalf("Create 失败: %v", err)
		}
		return sys.Stats()
	}
	base := measure()
	// 大量与 [50,60) 不相交的预约与其他变压器容量记录。
	for i := 0; i < 3000; i++ {
		if _, err := sys.Create("aux", 100000+i*10, 100000+i*10+5, 1); err != nil {
			t.Fatal(err)
		}
	}
	const records = 500
	for i := 1; i <= records; i++ {
		if err := sys.AddCapacityRecord(200000+i*100, 1<<30); err != nil {
			t.Fatal(err)
		}
	}
	after := measure()
	if base.SlotVisits != after.SlotVisits {
		t.Errorf("校验扫描槽数随不相交负载增长: 前 %d 后 %d", base.SlotVisits, after.SlotVisits)
	}
	// 容量表共 501 条记录，每次二分比较次数 <= ceil(log2(501)) = 9。
	maxCmp := 0
	for (1 << maxCmp) < records+1 {
		maxCmp++
	}
	if after.CapCompares > after.SlotVisits*maxCmp {
		t.Errorf("容量表查询超过对数上界: 比较 %d 次, 上界 %d", after.CapCompares, after.SlotVisits*maxCmp)
	}
	t.Logf("槽扫描 %d 次（与负载无关），容量比较 %d 次（对数上界 %d）",
		after.SlotVisits, after.CapCompares, after.SlotVisits*maxCmp)
}

// TestAdvanceSettleCost 验证推进时刻的落定开销只与本次落定的预约数相关。
func TestAdvanceSettleCost(t *testing.T) {
	sys := NewSystem(Config{HoldDuration: 10}, map[string]int{"f": 1 << 30}, 1<<30)
	for i := 0; i < 2000; i++ {
		if _, err := sys.Create("f", 100, 200, 1); err != nil { // 到期时刻均为 10
			t.Fatal(err)
		}
	}
	if err := sys.AdvanceTime(5); err != nil {
		t.Fatal(err)
	}
	if st := sys.Stats(); st.Settled != 0 || st.SettlePops != 0 {
		t.Errorf("无到期预约时不应有落定开销: settled=%d pops=%d", st.Settled, st.SettlePops)
	}
	if err := sys.AdvanceTime(10); err != nil {
		t.Fatal(err)
	}
	if st := sys.Stats(); st.Settled != 2000 || st.SettlePops != 2000 {
		t.Errorf("落定数应为 2000: settled=%d pops=%d", st.Settled, st.SettlePops)
	}
	// 已完成落定同样按个数计费。
	for i := 0; i < 3000; i++ {
		id, err := sys.Create("f", 20, 50, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := sys.Confirm(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := sys.AdvanceTime(49); err != nil {
		t.Fatal(err)
	}
	if st := sys.Stats(); st.Settled != 0 {
		t.Errorf("时刻 49 不应有完成落定: settled=%d", st.Settled)
	}
	if err := sys.AdvanceTime(50); err != nil {
		t.Fatal(err)
	}
	if st := sys.Stats(); st.Settled != 3000 || st.SettlePops != 3000 {
		t.Errorf("完成落定数应为 3000: settled=%d pops=%d", st.Settled, st.SettlePops)
	}
}
