package scheduler_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/scheduler"
	"ontology/scheduler/internal/naive"
)

// engine 统一生产实现与朴素模型的调用接口，便于逐事件对照。
type engine interface {
	add(id string, rtt, cwnd int64, role scheduler.Role) ([]scheduler.SentSegment, error)
	write(n int64) ([]scheduler.SentSegment, error)
	subAck(id string, cum int64) ([]scheduler.SentSegment, error)
	connAck(ack, win int64) ([]scheduler.SentSegment, error)
	fail(id string) ([]scheduler.SentSegment, error)
	recover(id string) ([]scheduler.SentSegment, error)
	setRTT(id string, rtt int64) ([]scheduler.SentSegment, error)
	snapshot() scheduler.State
}

type naiveEngine struct{ m *naive.Model }

func (n naiveEngine) add(id string, rtt, cwnd int64, role scheduler.Role) ([]scheduler.SentSegment, error) {
	return n.m.AddSubflow(id, rtt, cwnd, role)
}
func (n naiveEngine) write(bytes int64) ([]scheduler.SentSegment, error) { return n.m.Write(bytes) }
func (n naiveEngine) subAck(id string, cum int64) ([]scheduler.SentSegment, error) {
	return n.m.SubflowAck(id, cum)
}
func (n naiveEngine) connAck(ack, win int64) ([]scheduler.SentSegment, error) {
	return n.m.ConnAck(ack, win)
}
func (n naiveEngine) fail(id string) ([]scheduler.SentSegment, error) {
	return n.m.SubflowFail(id)
}
func (n naiveEngine) recover(id string) ([]scheduler.SentSegment, error) {
	return n.m.SubflowRecover(id)
}
func (n naiveEngine) setRTT(id string, rtt int64) ([]scheduler.SentSegment, error) {
	return n.m.SetRTT(id, rtt)
}
func (n naiveEngine) snapshot() scheduler.State { return n.m.Snapshot() }

// event 是一个可回放的事件。
type event struct {
	kind string
	id   string
	a    int64
	b    int64
	role scheduler.Role
}

func (e event) String() string {
	switch e.kind {
	case "add":
		role := "普通"
		if e.role == scheduler.Backup {
			role = "备用"
		}
		return fmt.Sprintf("添加子流 id=%q rtt=%dms cwnd=%d 角色=%s", e.id, e.a, e.b, role)
	case "write":
		return fmt.Sprintf("写入数据 %d 字节", e.a)
	case "subAck":
		return fmt.Sprintf("子流确认 id=%q 累计确认=%d", e.id, e.a)
	case "connAck":
		return fmt.Sprintf("连接级确认 确认序号=%d 通告窗口=%d", e.a, e.b)
	case "fail":
		return fmt.Sprintf("子流失效 id=%q", e.id)
	case "recover":
		return fmt.Sprintf("子流恢复 id=%q", e.id)
	case "setRTT":
		return fmt.Sprintf("修改往返时延 id=%q rtt=%dms", e.id, e.a)
	}
	return "未知事件"
}

// applyReal 在生产实现上回放事件，返回完整决策（含判定依据与阻塞说明）。
func (e event) applyReal(s *scheduler.Scheduler) (*scheduler.Decision, error) {
	switch e.kind {
	case "add":
		return s.AddSubflow(e.id, e.a, e.b, e.role)
	case "write":
		return s.Write(e.a)
	case "subAck":
		return s.SubflowAck(e.id, e.a)
	case "connAck":
		return s.ConnAck(e.a, e.b)
	case "fail":
		return s.SubflowFail(e.id)
	case "recover":
		return s.SubflowRecover(e.id)
	case "setRTT":
		return s.SetRTT(e.id, e.a)
	}
	panic("未知事件 " + e.kind)
}

func normSegs(s []scheduler.Seg) []scheduler.Seg {
	if len(s) == 0 {
		return nil
	}
	return s
}

// normState 归一化空切片（nil 与空切片视为等价），便于 DeepEqual。
func normState(st scheduler.State) scheduler.State {
	st.PendingReinject = normSegs(st.PendingReinject)
	st.PendingFresh = normSegs(st.PendingFresh)
	for i := range st.Subflows {
		st.Subflows[i].Inflight = normSegs(st.Subflows[i].Inflight)
	}
	if len(st.Subflows) == 0 {
		st.Subflows = nil
	}
	return st
}

func normSent(s []scheduler.SentSegment) []scheduler.SentSegment {
	out := make([]scheduler.SentSegment, len(s))
	for i, g := range s {
		g.Reason = "" // 判定依据仅用于日志，不参与等价比对
		out[i] = g
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func errCode(err error) scheduler.ErrorCode {
	code, ok := scheduler.CodeOf(err)
	if !ok {
		return scheduler.ErrorCode(-1)
	}
	return code
}

func subflowOf(st scheduler.State, id string) scheduler.SubflowState {
	for _, sf := range st.Subflows {
		if sf.ID == id {
			return sf
		}
	}
	return scheduler.SubflowState{}
}

// genEvent 基于真实调度器的当前状态生成一个随机事件，
// 以一定概率构造合法确认，也覆盖各类错误路径。
func genEvent(rng *rand.Rand, ids []string, added map[string]bool, st scheduler.State) event {
	if len(added) == 0 {
		return genAdd(rng, ids, added)
	}
	roll := rng.Intn(100)
	switch {
	case roll < 10 && len(added) < len(ids):
		return genAdd(rng, ids, added)
	case roll < 38:
		return event{kind: "write", a: 1 + rng.Int63n(500)}
	case roll < 58:
		return genSubAck(rng, ids, added, st)
	case roll < 72:
		return genConnAck(rng, st)
	case roll < 80:
		return event{kind: "fail", id: pickID(rng, ids, added)}
	case roll < 87:
		return event{kind: "recover", id: pickID(rng, ids, added)}
	default:
		return genSetRTT(rng, ids, added)
	}
}

func genAdd(rng *rand.Rand, ids []string, added map[string]bool) event {
	free := []string{}
	for _, id := range ids {
		if !added[id] {
			free = append(free, id)
		}
	}
	id := free[rng.Intn(len(free))]
	added[id] = true
	role := scheduler.Normal
	if rng.Intn(100) < 30 {
		role = scheduler.Backup
	}
	return event{kind: "add", id: id, a: 1 + rng.Int63n(60), b: 100 * (1 + rng.Int63n(8)), role: role}
}

func pickID(rng *rand.Rand, ids []string, added map[string]bool) string {
	if rng.Intn(100) < 5 {
		return "ghost" // 触发“子流不存在”
	}
	known := []string{}
	for _, id := range ids {
		if added[id] {
			known = append(known, id)
		}
	}
	return known[rng.Intn(len(known))]
}

func genSubAck(rng *rand.Rand, ids []string, added map[string]bool, st scheduler.State) event {
	id := pickID(rng, ids, added)
	sf := subflowOf(st, id)
	if sf.ID == "" || rng.Intn(100) < 40 {
		// 随机值：可能落在边界上，也可能触发非整段/越界/过期。
		return event{kind: "subAck", id: id, a: rng.Int63n(sf.SentBytes + 200)}
	}
	// 合法值：在途段的某个前缀边界（含 0 与全部在途）。
	k := rng.Intn(len(sf.Inflight) + 1)
	cum := sf.AckedBytes
	for i := 0; i < k; i++ {
		cum += sf.Inflight[i].Len
	}
	return event{kind: "subAck", id: id, a: cum}
}

func genConnAck(rng *rand.Rand, st scheduler.State) event {
	roll := rng.Intn(100)
	switch {
	case roll < 55:
		// 合法推进：确认序号在 [已确认, 已发送] 之间。
		ack := st.Acked
		if st.SentMax > st.Acked {
			ack = st.Acked + rng.Int63n(st.SentMax-st.Acked+1)
		}
		return event{kind: "connAck", a: ack, b: rng.Int63n(900)}
	case roll < 80:
		// 任意值：可能过期或越界。
		return event{kind: "connAck", a: rng.Int63n(st.SentMax + 200), b: rng.Int63n(900)}
	default:
		// 刻意过期（若已确认序号为 0 则退化为合法）。
		ack := int64(0)
		if st.Acked > 0 {
			ack = rng.Int63n(st.Acked)
		}
		return event{kind: "connAck", a: ack, b: rng.Int63n(900)}
	}
}

func genSetRTT(rng *rand.Rand, ids []string, added map[string]bool) event {
	id := pickID(rng, ids, added)
	if rng.Intn(100) < 5 {
		return event{kind: "setRTT", id: id, a: 0} // 触发“参数非法”
	}
	return event{kind: "setRTT", id: id, a: 1 + rng.Int63n(60)}
}

func formatSent(sent []scheduler.SentSegment) string {
	if len(sent) == 0 {
		return "（无发送）"
	}
	out := ""
	for i, g := range sent {
		if i > 0 {
			out += "; "
		}
		out += fmt.Sprintf("[%d,%d)->%q", g.Seq, g.Seq+g.Len, g.Subflow)
		if g.Resend {
			out += "(重新注入)"
		}
		if g.Reason != "" {
			out += " 「" + g.Reason + "」"
		}
	}
	return out
}

// TestRandomAgainstNaive 用 1000 组以上随机事件序列逐事件对照
// 生产实现与独立朴素模型：输出、错误分类与完整状态快照必须一致。
// 每步日志打印输入、输出、判定依据与对照结论。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1200
	const steps = 80
	ids := []string{"s0", "s1", "s2", "s3"}

	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		real, err := scheduler.New(100)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		model, err := naive.New(100)
		if err != nil {
			t.Fatalf("naive.New: %v", err)
		}
		ne := naiveEngine{model}
		added := map[string]bool{}

		for step := 0; step < steps; step++ {
			ev := genEvent(rng, ids, added, real.Snapshot())

			d, gotErr := ev.applyReal(real)
			var gotSent []scheduler.SentSegment
			blocked := ""
			if d != nil {
				gotSent = d.Sent
				blocked = d.Blocked
			}
			wantSent, wantErr := ev.apply(ne)

			log := fmt.Sprintf("seed=%d step=%d\n  输入: %s\n  输出: %s\n  阻塞: %s\n  错误: %v",
				seed, step, ev, formatSent(gotSent), blocked, gotErr)
			t.Log(log)

			if gotErr == nil && wantErr == nil {
				// 一致，继续比对输出与状态
			} else if gotErr == nil || wantErr == nil || errCode(gotErr) != errCode(wantErr) {
				t.Fatalf("%s\n  判定: 错误分类不一致 real=%v naive=%v", log, gotErr, wantErr)
			}
			if !reflect.DeepEqual(normSent(gotSent), normSent(wantSent)) {
				t.Fatalf("%s\n  判定: 发送输出不一致\n  real=%v\n  naive=%v", log, gotSent, wantSent)
			}
			gotState := normState(real.Snapshot())
			wantState := normState(ne.snapshot())
			if !reflect.DeepEqual(gotState, wantState) {
				t.Fatalf("%s\n  判定: 状态快照不一致\n  real=%+v\n  naive=%+v", log, gotState, wantState)
			}
			t.Logf("seed=%d step=%d 判定: 与朴素模型一致", seed, step)
		}
	}
}

func (e event) apply(eng engine) ([]scheduler.SentSegment, error) {
	switch e.kind {
	case "add":
		return eng.add(e.id, e.a, e.b, e.role)
	case "write":
		return eng.write(e.a)
	case "subAck":
		return eng.subAck(e.id, e.a)
	case "connAck":
		return eng.connAck(e.a, e.b)
	case "fail":
		return eng.fail(e.id)
	case "recover":
		return eng.recover(e.id)
	case "setRTT":
		return eng.setRTT(e.id, e.a)
	}
	panic("未知事件 " + e.kind)
}
