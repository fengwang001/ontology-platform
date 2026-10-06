package maintenance

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// worldState 为两种实现都要投影出的可比较状态。
type worldState struct {
	orders []orderSnap
	events []eventSnap
}

type orderSnap struct {
	id                        int
	level                     Level
	status                    Status
	assignedTo                int
	dispatchedAt, responseDue int
	completeDue               int
	rejects                   int
}

type eventSnap struct {
	at    int
	typ   EventType
	order int
	who   int
	lev   Level
}

func snapshotService(t *testing.T, s *Service, now int) worldState {
	t.Helper()
	st := worldState{}
	for id := 1; id <= s.nextOrderID; id++ {
		v, err := s.GetOrder(now, id)
		if err != nil {
			t.Fatalf("snapshot get %d: %v", id, err)
		}
		st.orders = append(st.orders, orderSnap{
			id: v.ID, level: v.Level, status: v.Status, assignedTo: v.AssignedTo,
			dispatchedAt: v.DispatchedAt, responseDue: v.ResponseDue,
			completeDue: v.CompletionDue, rejects: v.RejectCount,
		})
	}
	for _, e := range s.Events() {
		st.events = append(st.events, eventSnap{e.At, e.Type, e.OrderID, e.ContractorID, e.NewLevel})
	}
	return st
}

func snapshotNaive(m *naiveModel, now int) worldState {
	st := worldState{}
	for id := 1; id <= m.nextO; id++ {
		o := m.orders[id]
		status := o.status
		assigned := o.assignedTo
		if status == StatusDispatched && now > o.responseDue {
			status = StatusQueued
			assigned = 0
		} else if status == StatusConfirmed && now > o.completeDue {
			status = StatusOverdue
		}
		st.orders = append(st.orders, orderSnap{
			id: o.id, level: o.level, status: status, assignedTo: assigned,
			dispatchedAt: o.dispatchedAt, responseDue: o.responseDue,
			completeDue: o.completeDue, rejects: o.rejects,
		})
	}
	for _, e := range m.events {
		st.events = append(st.events, eventSnap{e.at, e.typ, e.order, e.who, e.newLev})
	}
	return st
}

func sameWorld(a, b worldState) (string, bool) {
	if len(a.orders) != len(b.orders) {
		return "order count differs", false
	}
	for i := range a.orders {
		if a.orders[i] != b.orders[i] {
			return fmt.Sprintf("order %d:\n impl=%+v\n naive=%+v",
				a.orders[i].id, a.orders[i], b.orders[i]), false
		}
	}
	if len(a.events) != len(b.events) {
		return fmt.Sprintf("event count %d vs %d", len(a.events), len(b.events)), false
	}
	for i := range a.events {
		if a.events[i] != b.events[i] {
			return fmt.Sprintf("event %d: impl=%+v naive=%+v", i, a.events[i], b.events[i]), false
		}
	}
	return "", true
}

func errCode(err error) string {
	for _, e := range []error{ErrInvalidArgument, ErrClockBackward, ErrNotFound,
		ErrInvalidState, ErrNoCandidate, ErrForbidden} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// opKind 为随机操作类型。
type opKind int

const (
	opRegister opKind = iota
	opSubmit
	opDispatch
	opConfirm
	opReject
	opComplete
	opCancel
	opDeactivate
	opTick
)

type op struct {
	kind     opKind
	now      int
	t1, t2   string
	level    Level
	cid, oid int
	tenant   string
	emerg    bool
}

func (o op) String() string {
	names := []string{"register", "submit", "dispatch", "confirm", "reject",
		"complete", "cancel", "deactivate", "tick"}
	return fmt.Sprintf("%s@%d cid=%d oid=%d lvl=%d t=%s/%s tenant=%s emerg=%v",
		names[o.kind], o.now, o.cid, o.oid, o.level, o.t1, o.t2, o.tenant, o.emerg)
}

// runOp 把同一操作施加到一个实现上，返回结果/错误的可比较描述。
func runService(s *Service, o op) string {
	switch o.kind {
	case opRegister:
		id, err := s.RegisterContractor(o.now, []string{o.t1}, []string{o.t2}, 1+o.cid%3, o.emerg)
		return fmt.Sprintf("id=%d err=%s", id, errCode(err))
	case opSubmit:
		id, err := s.SubmitOrder(o.now, o.tenant, o.t1, o.t2, o.level)
		return fmt.Sprintf("id=%d err=%s", id, errCode(err))
	case opDispatch:
		oid, cid, err := s.DispatchNext(o.now)
		return fmt.Sprintf("oid=%d cid=%d err=%s", oid, cid, errCode(err))
	case opConfirm:
		return "err=" + errCode(s.Confirm(o.now, o.oid, o.cid))
	case opReject:
		return "err=" + errCode(s.Reject(o.now, o.oid, o.cid))
	case opComplete:
		return "err=" + errCode(s.Complete(o.now, o.cid, o.oid))
	case opCancel:
		return "err=" + errCode(s.Cancel(o.now, o.tenant, o.oid))
	case opDeactivate:
		return "err=" + errCode(s.Deactivate(o.now, o.cid))
	default:
		return "err=" + errCode(s.Tick(o.now))
	}
}

func runNaive(m *naiveModel, o op) string {
	switch o.kind {
	case opRegister:
		id, err := m.register(o.now, []string{o.t1}, []string{o.t2}, 1+o.cid%3, o.emerg)
		return fmt.Sprintf("id=%d err=%s", id, errCode(err))
	case opSubmit:
		id, err := m.submit(o.now, o.tenant, o.t1, o.t2, o.level)
		return fmt.Sprintf("id=%d err=%s", id, errCode(err))
	case opDispatch:
		oid, cid, err := m.dispatch(o.now)
		return fmt.Sprintf("oid=%d cid=%d err=%s", oid, cid, errCode(err))
	case opConfirm:
		return "err=" + errCode(m.confirm(o.now, o.oid, o.cid))
	case opReject:
		return "err=" + errCode(m.reject(o.now, o.oid, o.cid))
	case opComplete:
		return "err=" + errCode(m.complete(o.now, o.cid, o.oid))
	case opCancel:
		return "err=" + errCode(m.cancel(o.now, o.tenant, o.oid))
	case opDeactivate:
		return "err=" + errCode(m.deactivate(o.now, o.cid))
	default:
		return "err=" + errCode(m.tick(o.now))
	}
}

// TestDifferentialRandom 用固定种子生成随机操作序列，逐步对照真实实现与
// 朴素模型；日志打印每步输入、两侧输出、以及状态一致/分歧判定依据。
// 通过 -v 可见完整逐步日志。
func TestDifferentialRandom(t *testing.T) {
	cfg := Config{
		ResponseLimit: [4]int{6, 5, 4, 3},
		CompleteLimit: [4]int{15, 12, 9, 7},
		RejectUpgrade: 2,
	}
	trades := []string{"pipe", "electric", "hvac"}
	buildings := []string{"A", "B", "C"}
	tenants := []string{"alice", "bob", "carol"}

	var log strings.Builder
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		svc := New(cfg)
		nav := newNaive(cfg)
		now := 0
		fmt.Fprintf(&log, "=== seed %d ===\n", seed)
		for step := 0; step < 180; step++ {
			// 时钟以大概率前进，小概率回退以触发时钟错误路径。
			if rng.Intn(8) != 0 {
				now += rng.Intn(4)
			} else {
				now -= rng.Intn(2)
				if now < 0 {
					now = 0
				}
			}
			o := op{kind: opKind(rng.Intn(int(opTick) + 1)), now: now}
			switch o.kind {
			case opRegister:
				o.t1 = trades[rng.Intn(len(trades))]
				o.t2 = buildings[rng.Intn(len(buildings))]
				o.emerg = rng.Intn(2) == 0
				o.cid = rng.Intn(3) // 仅用作容量扰动
			case opSubmit:
				o.tenant = tenants[rng.Intn(len(tenants))]
				o.t1 = trades[rng.Intn(len(trades))]
				o.t2 = buildings[rng.Intn(len(buildings))]
				o.level = Level(1 + rng.Intn(4))
			default:
				o.cid = 1 + rng.Intn(6)
				o.oid = 1 + rng.Intn(10)
				o.tenant = tenants[rng.Intn(len(tenants))]
			}

			r1 := runService(svc, o)
			r2 := runNaive(nav, o)
			agree := r1 == r2
			st1 := snapshotService(t, svc, max4(now, svc.lastNow))
			st2 := snapshotNaive(nav, max4(now, nav.now))
			why := "states match"
			stateOK := true
			if msg, ok := sameWorld(st1, st2); !ok {
				stateOK = false
				why = "DIVERGENCE: " + msg
			}
			fmt.Fprintf(&log, "step %3d | %-70s | impl=%-40s naive=%-40s | outputs=%v | %s\n",
				step, o.String(), r1, r2, agree, why)
			if !agree || !stateOK {
				t.Fatalf("seed %d step %d mismatch:\n%s\nop=%s\nimpl=%s\nnaive=%s",
					seed, step, why, o.String(), r1, r2)
			}
		}
	}
	t.Log("\n" + log.String())
}

func max4(a, b int) int {
	if a > b {
		return a
	}
	return b
}
