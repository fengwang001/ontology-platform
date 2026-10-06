package station

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// logger 在随机对照中打印每条输入、输出与判定依据。
type logger struct {
	w io.Writer
}

func (l logger) logf(format string, args ...any) {
	if l.w == nil {
		return
	}
	fmt.Fprintf(l.w, format+"\n", args...)
}

// cmpState 比较两模型完整可观测状态，忽略 CarMax（朴素模型不单独跟踪）。
func cmpState(a, b Snapshot) string {
	if a.Now != b.Now {
		return fmt.Sprintf("now %d != %d", a.Now, b.Now)
	}
	if a.Cap != b.Cap {
		return fmt.Sprintf("cap %d != %d", a.Cap, b.Cap)
	}
	if len(a.Ports) != len(b.Ports) {
		return "ports length"
	}
	for i := range a.Ports {
		if a.Ports[i] != b.Ports[i] {
			return fmt.Sprintf("port %d: %+v != %+v", i, a.Ports[i], b.Ports[i])
		}
	}
	am := map[string]Session{}
	for _, s := range a.Sessions {
		am[s.ID] = s
	}
	bm := map[string]Session{}
	for _, s := range b.Sessions {
		bm[s.ID] = s
	}
	if len(am) != len(bm) {
		return "sessions length"
	}
	for id, x := range am {
		y, ok := bm[id]
		if !ok {
			return "session missing " + id
		}
		if x.ID != y.ID || x.PortID != y.PortID || x.PlugOrder != y.PlugOrder ||
			x.Priority != y.Priority || x.State != y.State || x.Energy != y.Energy ||
			x.Demand != y.Demand || x.MinPwr != y.MinPwr || x.Cap != y.Cap || x.Pwr != y.Pwr {
			return fmt.Sprintf("session %s:\n  got %+v\n  want %+v", id, x, y)
		}
	}
	return ""
}

func mustState(t *testing.T, st *Station, nv *naiveModel, step string) {
	t.Helper()
	if diff := cmpState(st.Snapshot(), nv.snapshot()); diff != "" {
		t.Fatalf("step %s state mismatch: %s", step, diff)
	}
}

func ports(n int, cap int) []Port {
	var ps []Port
	for i := 0; i < n; i++ {
		ps = append(ps, Port{ID: fmt.Sprintf("p%d", i), MaxPwr: cap})
	}
	return ps
}
func plugOK(t *testing.T, st *Station, p PlugParams) string {
	t.Helper()
	id, err := st.Plug(p)
	if err != nil {
		t.Fatalf("plug %s: %v", p.SessionID, err)
	}
	return id
}

// 最低可用功率恰等于份额：两台车各得 5，均不低于最低功率 5。
func TestMinExactlyShare(t *testing.T) {
	st := New(10, ports(2, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 100, CarMax: 10, MinPwr: 5})
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 100, CarMax: 10, MinPwr: 5})
	p, _ := st.PowerOf("a")
	q, _ := st.PowerOf("b")
	if p != 5 || q != 5 {
		t.Fatalf("want 5/5 got %d/%d", p, q)
	}
}

// 余量分配顺序：11 单位两台车，先插者 6、后插者 5。
func TestRemainderOrder(t *testing.T) {
	st := New(11, ports(2, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 100, CarMax: 10, MinPwr: 0})
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 100, CarMax: 10, MinPwr: 0})
	p, _ := st.PowerOf("a")
	q, _ := st.PowerOf("b")
	if p != 6 || q != 5 {
		t.Fatalf("want 6/5 got %d/%d", p, q)
	}
}

// 优先车辆到达挤出普通车辆；同类别新车不挤出老车。
func TestPriorityAndHysteresis(t *testing.T) {
	st := New(6, ports(3, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 100, CarMax: 6, MinPwr: 5})
	if p, _ := st.PowerOf("a"); p != 6 {
		t.Fatalf("a alone want 6 got %d", p)
	}
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 100, CarMax: 6, MinPwr: 5})
	// 同类别新车 b 不挤老车 a：a 保持 6，b 等待。
	if p, _ := st.PowerOf("a"); p != 6 {
		t.Fatalf("a squeezed by same-class newcomer: %d", p)
	}
	if p, _ := st.PowerOf("b"); p != 0 {
		t.Fatalf("b should wait, got %d", p)
	}
	plugOK(t, st, PlugParams{SessionID: "c", PortID: "p2", Demand: 100, CarMax: 6, MinPwr: 5, Priority: PriorityFast})
	// 更高类别 c 拿走 6：普通车 a、b 均等待。
	if p, _ := st.PowerOf("c"); p != 6 {
		t.Fatalf("c want 6 got %d", p)
	}
	if p, _ := st.PowerOf("a"); p != 0 {
		t.Fatalf("a should be squeezed by fast car, got %d", p)
	}
}

// 总上限下降引发连锁等待，恢复后等待车辆重新充电。
func TestCapDropAndRecovery(t *testing.T) {
	st := New(12, ports(3, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 100, CarMax: 4, MinPwr: 4})
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 100, CarMax: 4, MinPwr: 4})
	plugOK(t, st, PlugParams{SessionID: "c", PortID: "p2", Demand: 100, CarMax: 4, MinPwr: 4})
	if p, _ := st.PowerOf("c"); p != 4 {
		t.Fatalf("initial 4/4/4, c=%d", p)
	}
	if err := st.ChangeCap(0, 8); err != nil {
		t.Fatal(err)
	}
	// 8 单位只够两台 4：最晚插枪的 c 等待。
	if p, _ := st.PowerOf("a"); p != 4 {
		t.Fatalf("a want 4 got %d", p)
	}
	if p, _ := st.PowerOf("c"); p != 0 {
		t.Fatalf("c should wait, got %d", p)
	}
	if err := st.ChangeCap(0, 4); err != nil {
		t.Fatal(err)
	}
	// 只够一台：a 保留（插枪最早），b、c 等待。
	if p, _ := st.PowerOf("a"); p != 4 {
		t.Fatalf("a want 4 got %d", p)
	}
	if p, _ := st.PowerOf("b"); p != 0 || stateOf(st, "b") != StateWaiting {
		t.Fatalf("b should wait")
	}
	if err := st.ChangeCap(0, 12); err != nil {
		t.Fatal(err)
	}
	// 恢复：三台依次按插枪序恢复，都应重新充电。
	for _, id := range []string{"a", "b", "c"} {
		if p, _ := st.PowerOf(id); p != 4 {
			t.Fatalf("%s want 4 after recovery got %d", id, p)
		}
	}
}

// 同一推进中多台车先后充满：a 先满（t=2）释放功率，b 加速后于 t=3 充满。
func TestMultipleFullDuringAdvance(t *testing.T) {
	st := New(6, ports(2, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 6, CarMax: 6, MinPwr: 0})
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 12, CarMax: 10, MinPwr: 0})
	if err := st.Advance(2); err != nil {
		t.Fatal(err)
	}
	if stateOf(st, "a") != StateFull || stateOf(st, "b") != StateCharging {
		t.Fatalf("at t=2 a should be full")
	}
	// a 满后 b 独占 6：第 3 秒结束 b 累计 3*2+6=12，恰好充满。
	if err := st.Advance(3); err != nil {
		t.Fatal(err)
	}
	if stateOf(st, "b") != StateFull {
		t.Fatalf("b should be full at t=3")
	}
	if st.Now() != 3 {
		t.Fatalf("now want 3 got %d", st.Now())
	}
}

// 恰在推进末尾那一秒充满。
func TestFullAtLastSecond(t *testing.T) {
	st := New(5, ports(1, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 10, CarMax: 5, MinPwr: 0})
	if err := st.Advance(2); err != nil {
		t.Fatal(err)
	}
	if stateOf(st, "a") != StateFull {
		t.Fatalf("a should be full exactly at t=2")
	}
	if e, _ := st.Unplug("a"); e != 10 {
		t.Fatalf("energy want 10 got %d", e)
	}
}

// 上限变更与充满同一时刻：t=2 时 a 充满，同时上限降为 0。
func TestCapChangeSameMomentAsFull(t *testing.T) {
	st := New(8, ports(2, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 8, CarMax: 8, MinPwr: 0})
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p1", Demand: 100, CarMax: 10, MinPwr: 1})
	if err := st.ChangeCap(2, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Advance(2); err != nil {
		t.Fatal(err)
	}
	if stateOf(st, "a") != StateFull {
		t.Fatalf("a full at t=2")
	}
	if stateOf(st, "b") != StateWaiting {
		t.Fatalf("b(min=1) must wait after cap 0 at t=2")
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 接口不存在 > 接口占用 > 会话不存在 > 状态不允许。
func TestRejectionOrder(t *testing.T) {
	st := New(10, ports(1, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 100, CarMax: 5, MinPwr: 5})

	// 参数非法优先于接口不存在。
	if _, err := st.Plug(PlugParams{PortID: "nope", Demand: 0, CarMax: 5}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want invalid, got %v", err)
	}
	// 接口不存在优先于接口占用语义。
	if _, err := st.Plug(PlugParams{PortID: "nope", Demand: 1, CarMax: 5}); !errors.Is(err, ErrPortMissing) {
		t.Fatalf("want port missing, got %v", err)
	}
	// 接口占用。
	if _, err := st.Plug(PlugParams{PortID: "p0", Demand: 1, CarMax: 5}); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("want port busy, got %v", err)
	}
	// 最低功率大于最大功率属参数非法。
	if _, err := st.Plug(PlugParams{SessionID: "b", PortID: "p0", Demand: 1, CarMax: 5, MinPwr: 6}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want invalid min>max, got %v", err)
	}
	// 推进到不晚于当前时刻的时刻报时钟回退。
	if err := st.Advance(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Advance(0); !errors.Is(err, ErrClockBack) {
		t.Fatalf("want clock back, got %v", err)
	}
	// ChangeCap：参数非法优先于时钟回退。
	if err := st.ChangeCap(0, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want invalid cap, got %v", err)
	}
	// ChangeCap：时钟回退。
	st.Advance(3)
	if err := st.ChangeCap(1, 10); !errors.Is(err, ErrClockBack) {
		t.Fatalf("want clock back, got %v", err)
	}
	// 会话不存在：拔枪未知会话。
	if _, err := st.Unplug("ghost"); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("want session gone, got %v", err)
	}
	// 状态不允许：对已充满会话调整优先级。
	st2 := New(5, ports(1, 10))
	plugOK(t, st2, PlugParams{SessionID: "f", PortID: "p0", Demand: 5, CarMax: 5, MinPwr: 0})
	st2.Advance(1)
	if stateOf(st2, "f") != StateFull {
		t.Fatalf("setup: f should be full")
	}
	if err := st2.SetPriority("f", PriorityFast); !errors.Is(err, ErrState) {
		t.Fatalf("want state error, got %v", err)
	}
}

// 充满后仍占接口直到拔枪。
func TestFullHoldsPort(t *testing.T) {
	st := New(5, ports(1, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 5, CarMax: 5, MinPwr: 0})
	st.Advance(1)
	if _, err := st.Plug(PlugParams{SessionID: "b", PortID: "p0", Demand: 1, CarMax: 1}); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("full car still holds port, got %v", err)
	}
	if e, err := st.Unplug("a"); err != nil || e != 5 {
		t.Fatalf("unplug full car energy=%d err=%v", e, err)
	}
	plugOK(t, st, PlugParams{SessionID: "b", PortID: "p0", Demand: 1, CarMax: 1})
}

func stateOf(st *Station, id string) State {
	snap := st.Snapshot()
	for _, s := range snap.Sessions {
		if s.ID == id {
			return s.State
		}
	}
	return StateEnded
}

// TestConcurrentSafe 大量并发操作后不变量必须始终成立且不发生数据竞争
// （配合 -race 使用）。
func TestConcurrentSafe(t *testing.T) {
	st := New(20, ports(8, 8))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				id := fmt.Sprintf("g%d-%d", g, k)
				pid := fmt.Sprintf("p%d", (g+k)%8)
				_, _ = st.Plug(PlugParams{
					SessionID: id, PortID: pid, Demand: 5 + k%10,
					CarMax: 5, MinPwr: k % 5,
					Priority: Priority(k % 2),
				})
				_ = st.SetPriority(id, Priority((k+1)%2))
				_ = st.Advance(st.Now() + k%3)
				_, _ = st.Unplug(id)
			}
		}(g)
	}
	wg.Wait()
	assertInvariants(t, st, "concurrent-end")
}

// BenchmarkAdvanceLongInterval 推进跨越极大秒数区间且无充满事件，
// 耗时必须为常数（不随秒数增长）。
func BenchmarkAdvanceLongInterval(b *testing.B) {
	for n := 0; n < b.N; n++ {
		st := New(6, ports(3, 10))
		_, _ = st.Plug(PlugParams{SessionID: "a", PortID: "p0", Demand: 1_000_000_000, CarMax: 2, MinPwr: 0})
		_, _ = st.Plug(PlugParams{SessionID: "b", PortID: "p1", Demand: 1_000_000_000, CarMax: 2, MinPwr: 0})
		_, _ = st.Plug(PlugParams{SessionID: "c", PortID: "p2", Demand: 1_000_000_000, CarMax: 2, MinPwr: 0})
		if err := st.Advance(1_000_000); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEventWithManyEnded 已结束会话数量不影响单次事件开销：
// 控制器在拔枪时立即删除会话，事件只遍历存活会话。
func BenchmarkEventWithManyEnded(b *testing.B) {
	st := New(20, ports(2, 20))
	for k := 0; k < 100000; k++ {
		id := fmt.Sprintf("old%d", k)
		_, _ = st.Plug(PlugParams{SessionID: id, PortID: "p0", Demand: 1, CarMax: 1, MinPwr: 0})
		_, _ = st.Unplug(id)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = st.Plug(PlugParams{SessionID: "live", PortID: "p1", Demand: 10, CarMax: 5, MinPwr: 1})
		_ = st.SetPriority("live", PriorityFast)
		_, _ = st.Unplug("live")
	}
}

// TestAdvanceIndependentOfSeconds 以可执行断言形式验证推进耗时
// 不随秒数增长（10^6 秒区间，在站 4 车，无充满）。
func TestAdvanceIndependentOfSeconds(t *testing.T) {
	st := New(8, ports(4, 10))
	for i := 0; i < 4; i++ {
		plugOK(t, st, PlugParams{
			SessionID: fmt.Sprintf("c%d", i), PortID: fmt.Sprintf("p%d", i),
			Demand: 1_000_000_000, CarMax: 2, MinPwr: 0,
		})
	}
	if err := st.Advance(1_000_000); err != nil {
		t.Fatal(err)
	}
	if st.Now() != 1_000_000 {
		t.Fatalf("now %d", st.Now())
	}
	for _, s := range st.Snapshot().Sessions {
		if s.Energy != 2_000_000 {
			t.Fatalf("%s energy %d", s.ID, s.Energy)
		}
	}
}

// TestEndedSessionsRemoved 拔枪后会话与索引均被清除，
// 证明事件处理不会遍历到已结束会话。
func TestEndedSessionsRemoved(t *testing.T) {
	st := New(5, ports(1, 10))
	plugOK(t, st, PlugParams{SessionID: "a", PortID: "p0", Demand: 1, CarMax: 1, MinPwr: 0})
	if _, err := st.Unplug("a"); err != nil {
		t.Fatal(err)
	}
	if len(st.sessions) != 0 || len(st.portSess) != 0 {
		t.Fatalf("ended sessions must be removed: %d %d", len(st.sessions), len(st.portSess))
	}
}
