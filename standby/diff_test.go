package standby

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// op 是随机操作序列中的一步（两种系统收到完全相同的输入）。
type op struct {
	kind   string
	flight string
	cabin  string
	pax    string
	party  int
	prio   Priority
	id     EntryID
	cap    int
	now    int64
}

func runOp(t *testing.T, real *System, nv *naiveSystem, o op) (any, any, string) {
	t.Helper()
	var r1, r2 any
	var reason string
	switch o.kind {
	case "create":
		e1 := real.CreateFlight(o.flight, o.cabin, o.cap, 3, int64(o.party), o.now)
		e2 := nv.create(o.flight, o.cabin, o.cap, 3, int64(o.party), o.now)
		r1, r2, reason = e1, e2, fmt.Sprintf("CreateFlight cap=%d delay=%d", o.cap, o.party)
	case "register":
		// 先真实执行；朴素侧用 prepareRegister 复现拒绝判定，成功时用同一 id 提交。
		id1, e1 := real.Register(o.flight, o.cabin, o.pax, o.party, o.prio, o.now)
		commit, e2 := nv.prepareRegister(o.flight, o.cabin, o.pax, o.party, o.prio, o.now)
		var id2 EntryID
		if e1 == nil {
			if e2 != nil || commit == nil {
				e2 = ErrDuplicate // 触发下文错误不一致报告
			} else {
				id2 = id1
				commit(id1)
			}
		}
		r1, r2 = idErr{id1, e1}, idErr{id2, e2}
		reason = fmt.Sprintf("Register pax=%s party=%d prio=%d", o.pax, o.party, o.prio)
	case "withdraw", "confirm", "cancel":
		var e1 error
		switch o.kind {
		case "withdraw":
			e1 = real.Withdraw(o.id, o.now)
		case "confirm":
			e1 = real.Confirm(o.id, o.now)
		case "cancel":
			e1 = real.CancelConfirmed(o.id, o.now)
		}
		e2 := nv.entryOp(o.id, o.now, o.kind)
		r1, r2, reason = e1, e2, o.kind+" id="+fmt.Sprint(o.id)
	case "prio":
		e1 := real.ChangePriority(o.id, o.prio, o.now)
		e2 := nv.changePriority(o.id, o.prio, o.now)
		r1, r2, reason = e1, e2, "ChangePriority id="+fmt.Sprint(o.id)+" prio="+fmt.Sprint(o.prio)
	case "setcap":
		e1 := real.SetCapacity(o.flight, o.cabin, o.cap, o.now)
		e2 := nv.setCapacity(o.flight, o.cabin, o.cap, o.now)
		r1, r2, reason = e1, e2, "SetCapacity cap="+fmt.Sprint(o.cap)
	case "cancelFlight":
		e1 := real.CancelFlight(o.flight, o.cabin, o.now)
		e2 := nv.cancelFlight(o.flight, o.cabin, o.now)
		r1, r2, reason = e1, e2, "CancelFlight"
	case "snapshot":
		s1, e1 := real.Snapshot(o.flight, o.cabin, o.now)
		s2, e2 := nv.snapshot(o.flight, o.cabin, o.now)
		r1, r2, reason = snapRes{s1, e1}, snapRes{s2, e2}, "Snapshot"
	}
	return r1, r2, reason
}

type idErr struct {
	id EntryID
	e  error
}
type snapRes struct {
	s FlightSnapshot
	e error
}

// TestDifferentialRandom 用朴素模型比对大量随机操作序列。
// 每一步都打印输入、两边输出与判定依据；出错时可按固定种子完整重放。
func TestDifferentialRandom(t *testing.T) {
	const seeds = 2000
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		real := New()
		nv := newNaive()
		var logb strings.Builder
		// idPool 按“登记成功”顺序保存两边对应的 id 对（该模型中两边 id 恰好一致，
		// 但仍显式对齐，避免依赖实现细节）。
		type idPair struct{ r, n EntryID }
		var idPool []idPair

		flights := []string{"F1", "F2"}
		paxNames := []string{"a", "b", "c", "d", "e"}
		steps := 60
		for step := 0; step < steps; step++ {
			o := op{now: int64(step / 2)} // 时间缓步前进，制造同刻与过期
			k := rng.Intn(10)
			switch k {
			case 0:
				o.kind = "create"
				o.flight = flights[rng.Intn(len(flights))]
				o.cabin = "Y"
				o.cap = 1 + rng.Intn(6)
				o.party = 1 + rng.Intn(8) // 借字段传 delay
			case 1, 2, 3:
				o.kind = "register"
				o.flight = flights[rng.Intn(len(flights))]
				o.cabin = "Y"
				o.pax = paxNames[rng.Intn(len(paxNames))]
				o.party = 1 + rng.Intn(9)
				o.prio = Priority(rng.Intn(3))
			case 4, 5:
				o.kind = []string{"withdraw", "confirm"}[rng.Intn(2)]
			case 6:
				o.kind = "prio"
				o.prio = Priority(rng.Intn(3))
			case 7:
				o.kind = "cancel"
			case 8:
				o.kind = "setcap"
				o.cap = 1 + rng.Intn(8)
			case 9:
				o.kind = []string{"snapshot", "cancelFlight"}[rng.Intn(2)]
				o.flight = flights[rng.Intn(len(flights))]
				o.cabin = "Y"
			}
			if o.flight == "" {
				o.flight = flights[rng.Intn(len(flights))]
				o.cabin = "Y"
			}
			// 选取已有 id 供条目类操作使用（用真实系统中存在的 id，朴素侧经 idMap 对齐）。
			if o.kind == "withdraw" || o.kind == "confirm" || o.kind == "prio" || o.kind == "cancel" {
				if len(idPool) == 0 {
					continue
				}
				o.id = idPool[rng.Intn(len(idPool))].r
			}
			naiveID := o.id
			for _, p := range idPool {
				if p.r == o.id {
					naiveID = p.n
				}
			}
			o.id = naiveID
			r1, r2, reason := runOp(t, real, nv, o)

			line := fmt.Sprintf("seed=%d step=%d t=%d %s => real=%v naive=%v",
				seed, step, o.now, reason, fmtRes(r1), fmtRes(r2))
			t.Log(line)
			fmt.Fprintln(&logb, line)
			if seed == 86 {
				for _, fk := range []string{"F1", "F2"} {
					if fx := real.flights[flightKeyOf(fk, "Y")]; fx != nil {
						for _, en := range fx.entries {
							detail := fmt.Sprintf("        real %s#%d %s %s party=%d d=%d",
								fk, en.ID, en.State, en.Passenger, en.Party, en.Deadline)
							fmt.Fprintln(&logb, detail)
						}
					}
				}
			}

			// 结果对齐：错误类别一致；登记成功时记录 id 映射；快照逐字段一致。
			switch v1 := r1.(type) {
			case idErr:
				v2 := r2.(idErr)
				if !sameErr(v1.e, v2.e) {
					t.Fatalf("seed=%d step=%d %s: error mismatch real=%v naive=%v\n%s",
						seed, step, reason, v1.e, v2.e, logb.String())
				}
				if v1.e == nil {
					idPool = append(idPool, idPair{v1.id, v2.id})
				}
			case snapRes:
				v2 := r2.(snapRes)
				if !sameErr(v1.e, v2.e) {
					t.Fatalf("seed=%d snapshot error mismatch: %v %v\n%s", seed, v1.e, v2.e, logb.String())
				}
				if v1.e == nil && !reflect.DeepEqual(v1.s, v2.s) {
					t.Fatalf("seed=%d snapshot mismatch:\nreal=%+v\nnaive=%+v\n%s",
						seed, v1.s, v2.s, logb.String())
				}
			default:
				if !sameErr(asErr(r1), asErr(r2)) {
					t.Fatalf("seed=%d step=%d %s error mismatch real=%v naive=%v\n%s",
						seed, step, reason, asErr(r1), asErr(r2), logb.String())
				}
			}
		}
		// 序列末尾再对每个航班做一次远未来快照，强制结算所有过期并比对终态。
		for _, fl := range flights {
			o := op{kind: "snapshot", flight: fl, cabin: "Y", now: 1 << 40}
			r1, r2, _ := runOp(t, real, nv, o)
			if !reflect.DeepEqual(r1.(snapRes).s, r2.(snapRes).s) {
				t.Fatalf("seed=%d final snapshot mismatch:\nreal=%+v\nnaive=%+v\n%s",
					seed, r1.(snapRes).s, r2.(snapRes).s, logb.String())
			}
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Error() == b.Error()
}

func asErr(v any) error {
	if e, ok := v.(error); ok {
		return e
	}
	return nil
}

func fmtRes(v any) string {
	switch x := v.(type) {
	case idErr:
		if x.e != nil {
			return "ERR:" + x.e.Error()
		}
		return fmt.Sprintf("id=%d", x.id)
	case snapRes:
		if x.e != nil {
			return "ERR:" + x.e.Error()
		}
		return fmt.Sprintf("snap{conf=%d pend=%d wait=%d free=%d canceled=%v}",
			x.s.Confirmed, x.s.Pending, x.s.WaitingCount, x.s.Free, x.s.Canceled)
	case error:
		if x == nil {
			return "ok"
		}
		return "ERR:" + x.Error()
	}
	return fmt.Sprint(v)
}
