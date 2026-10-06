package station

import (
	"errors"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// op 是随机操作序列中的一条操作。
type op struct {
	kind   string
	id     string
	port   string
	demand int
	carMax int
	minPwr int
	pri    Priority
	t      int
	newCap int
}

func runOp(st *Station, nv *naiveModel, o op) (string, bool, bool) {
	switch o.kind {
	case "plug":
		id1, e1 := st.Plug(PlugParams{
			SessionID: o.id, PortID: o.port, Demand: o.demand, CarMax: o.carMax,
			MinPwr: o.minPwr, Priority: o.pri,
		})
		id2, ok2 := nv.plug(PlugParams{
			SessionID: o.id, PortID: o.port, Demand: o.demand, CarMax: o.carMax,
			MinPwr: o.minPwr, Priority: o.pri,
		})
		return "plug: id1=" + id1 + " id2=" + id2, e1 == nil, ok2
	case "unplug":
		e1, err1 := st.Unplug(o.id)
		e2, ok2 := nv.unplug(o.id)
		return "unplug: e1=" + strconv.Itoa(e1) + " e2=" + strconv.Itoa(e2),
			err1 == nil, ok2 && e1 == e2
	case "pri":
		err1 := st.SetPriority(o.id, o.pri)
		ok2 := nv.setPriority(o.id, o.pri)
		return "setpri", err1 == nil, ok2
	case "cap":
		err1 := st.ChangeCap(o.t, o.newCap)
		ok2 := nv.changeCap(o.t, o.newCap)
		return "cap", err1 == nil, ok2
	default:
		err1 := st.Advance(o.t)
		ok2 := nv.advance(o.t)
		return "advance", err1 == nil, ok2
	}
}

func genOps(r *rand.Rand, portCount int) []op {
	var ops []op
	now := 0
	n := 40 + r.Intn(60)
	usedPorts := map[string]bool{}
	idCount := 0
	for i := 0; i < n; i++ {
		k := r.Intn(100)
		switch {
		case k < 34:
			var free []string
			for j := 0; j < portCount; j++ {
				pid := "p" + strconv.Itoa(j)
				if !usedPorts[pid] {
					free = append(free, pid)
				}
			}
			if len(free) == 0 {
				i--
				continue
			}
			pid := free[r.Intn(len(free))]
			usedPorts[pid] = true
			idCount++
			id := "c" + strconv.Itoa(idCount)
			carMax := 1 + r.Intn(9)
			minPwr := r.Intn(carMax + 1)
			demand := 1 + r.Intn(24)
			pri := PriorityNormal
			if r.Intn(3) == 0 {
				pri = PriorityFast
			}
			// 插枪时端口在拔枪前不会复用；记录释放由 unplug 分支模拟。
			ops = append(ops, op{kind: "plug", id: id, port: pid,
				demand: demand, carMax: carMax, minPwr: minPwr, pri: pri})
			// 用一个伴随 map 跟踪释放由 execute 维护，这里简单随机安排拔枪。
		case k < 52:
			ids := pluggedIDs(ops)
			if len(ids) == 0 {
				i--
				continue
			}
			id := ids[r.Intn(len(ids))]
			delete(usedPorts, portOf(ops, id))
			ops = append(ops, op{kind: "unplug", id: id})
		case k < 60:
			ids := pluggedIDs(ops)
			if len(ids) == 0 {
				i--
				continue
			}
			id := ids[r.Intn(len(ids))]
			pri := PriorityNormal
			if r.Intn(2) == 0 {
				pri = PriorityFast
			}
			ops = append(ops, op{kind: "pri", id: id, pri: pri})
		case k < 70:
			now += r.Intn(6)
			newCap := r.Intn(20)
			ops = append(ops, op{kind: "cap", t: now, newCap: newCap})
		default:
			now += r.Intn(5)
			ops = append(ops, op{kind: "advance", t: now})
		}
	}
	return ops
}

func pluggedIDs(ops []op) []string {
	plugged := map[string]bool{}
	for _, o := range ops {
		if o.kind == "plug" {
			plugged[o.id] = true
		}
		if o.kind == "unplug" {
			delete(plugged, o.id)
		}
	}
	var ids []string
	for id := range plugged {
		ids = append(ids, id)
	}
	return ids
}

func portOf(ops []op, id string) string {
	for _, o := range ops {
		if o.kind == "plug" && o.id == id {
			return o.port
		}
	}
	return ""
}

func assertInvariants(t *testing.T, st *Station, step string) {
	t.Helper()
	snap := st.Snapshot()
	total := 0
	for _, s := range snap.Sessions {
		if s.State == StateCharging {
			if s.Pwr < s.MinPwr {
				t.Fatalf("step %s: %s charging pwr %d < min %d", step, s.ID, s.Pwr, s.MinPwr)
			}
			if s.Pwr > s.Cap {
				t.Fatalf("step %s: %s pwr %d > cap %d", step, s.ID, s.Pwr, s.Cap)
			}
		}
		if s.Pwr > 0 && s.State != StateCharging {
			t.Fatalf("step %s: %s non-charging with pwr %d", step, s.ID, s.Pwr)
		}
		if s.Energy > s.Demand {
			t.Fatalf("step %s: %s energy %d > demand %d", step, s.ID, s.Energy, s.Demand)
		}
		if s.State == StateFull && s.Energy != s.Demand {
			t.Fatalf("step %s: %s full energy %d != demand %d", step, s.ID, s.Energy, s.Demand)
		}
		total += s.Pwr
	}
	if total > snap.Cap {
		t.Fatalf("step %s: total %d > cap %d", step, total, snap.Cap)
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	// STATION_LOG=path 将每条输入/输出/判定依据写入文件；-v 时写入测试日志。
	logPath := os.Getenv("STATION_LOG")
	var l logger
	if logPath != "" {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		l.w = f
	} else {
		l.w = testWriter{t}
	}

	for iter := 0; iter < 300; iter++ {
		seed := int64(1000 + iter)
		r1 := rand.New(rand.NewSource(seed))
		portCount := 2 + r1.Intn(4)
		portCap := 3 + r1.Intn(8)
		ps := ports(portCount, portCap)
		initialCap := r1.Intn(22)
		ops := genOps(r1, portCount)

		st := New(initialCap, clonePorts(ps))
		nv := newNaive(initialCap, ps)
		l.logf("=== iter %d seed %d ports=%d portCap=%d cap=%d ops=%d ===",
			iter, seed, portCount, portCap, initialCap, len(ops))

		for i, o := range ops {
			detail, ok1, ok2 := runOp(st, nv, o)
			l.logf("[%03d] INPUT  %+v", i, o)
			l.logf("[%03d] OUTPUT %s accepted=%v naive=%v", i, detail, ok1, ok2)
			if ok1 != ok2 {
				t.Fatalf("seed %d step %d acceptance differs: %+v got=%v want=%v",
					seed, i, o, ok1, ok2)
			}
			if diff := cmpState(st.Snapshot(), nv.snapshot()); diff != "" {
				l.logf("[%03d] JUDGE  MISMATCH %s", i, diff)
				t.Fatalf("seed %d step %d op %+v state mismatch: %s", seed, i, o, diff)
			}
			assertInvariants(t, st, "seed-"+strconv.FormatInt(seed, 10)+"-step-"+strconv.Itoa(i))
			l.logf("[%03d] JUDGE  match; now=%d cap=%d", i, st.Now(), st.Snapshot().Cap)
		}

		// 确定性重放：同样的序列必须得到完全相同结果。
		rb1 := replayBytes(initialCap, ps, ops)
		rb2 := replayBytes(initialCap, ps, ops)
		if string(rb1) != string(rb2) {
			t.Fatalf("seed %d nondeterministic replay", seed)
		}
	}
}

func replayBytes(cap0 int, ps []Port, ops []op) []byte {
	st := New(cap0, clonePorts(ps))
	nv := newNaive(cap0, ps)
	buf := []byte{}
	for _, o := range ops {
		_, ok1, ok2 := runOp(st, nv, o)
		snap := st.Snapshot()
		b1, b2 := byte(0), byte(0)
		if ok1 {
			b1 = 1
		}
		if ok2 {
			b2 = 1
		}
		buf = append(buf, b1, b2, byte(snap.Now), byte(snap.Cap))
		for _, s := range snap.Sessions {
			buf = append(buf, []byte(s.ID)...)
			buf = append(buf, byte(s.State), byte(s.Pwr), byte(s.Priority))
			buf = append(buf, byte(s.Energy))
		}
		buf = append(buf, 255)
	}
	return buf
}

func clonePorts(ps []Port) []Port {
	out := make([]Port, len(ps))
	copy(out, ps)
	return out
}

// TestErrorTypes 确保各错误均为可区分的哨兵错误。
func TestErrorTypes(t *testing.T) {
	errs := []error{ErrInvalidParam, ErrClockBack, ErrPortMissing, ErrPortBusy, ErrSessionGone, ErrState}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("error categories not distinct: %v %v", errs[i], errs[j])
			}
		}
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
