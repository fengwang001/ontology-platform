package eventbus

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustSubscribe(t *testing.T, bus *Bus, topic, id string, priority int, fn Handler) {
	t.Helper()
	if err := bus.Subscribe(topic, id, priority, fn); err != nil {
		t.Fatalf("订阅 %s/%s 失败: %v", topic, id, err)
	}
}

// TestReentrantPublishIsBreadthFirst 验证重入发布排在当前事件所有处理器
// 之后（广度优先），而不是立即嵌套派发。
func TestReentrantPublishIsBreadthFirst(t *testing.T) {
	bus := NewBus(16)
	var calls []string
	mustSubscribe(t, bus, "t", "A", 5, func(topic string, payload any) {
		calls = append(calls, "A")
		if _, err := bus.Publish("u", "reentrant"); err != nil {
			t.Errorf("重入发布失败: %v", err)
		}
	})
	mustSubscribe(t, bus, "t", "B", 5, func(topic string, payload any) {
		calls = append(calls, "B")
	})
	mustSubscribe(t, bus, "u", "U", 1, func(topic string, payload any) {
		calls = append(calls, "U")
	})

	res, err := bus.Publish("t", "first")
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	want := []string{"A", "B", "U"}
	t.Logf("输入: 订阅 t:[A(5),B(5)] u:[U(1)]；A 内重入发布 u；发布 t")
	t.Logf("输出: 调用序列=%v, 结果=%+v", calls, res)
	t.Logf("判定依据: 重入事件 u 排在当前事件 t 的全部处理器(A,B)之后，即 [A B U]；本次调用共派发 2 个事件")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, want)
	}
	if !res.Dispatched || res.Count != 2 || res.Seq != 1 {
		t.Fatalf("PublishResult = %+v, 期望 {Seq:1 Dispatched:true Count:2}", res)
	}
}

// TestUnsubscribedAfterSnapshotSkipped 验证快照后被退订的处理器在轮到它时被跳过。
func TestUnsubscribedAfterSnapshotSkipped(t *testing.T) {
	bus := NewBus(16)
	var calls []string
	mustSubscribe(t, bus, "t", "A", 9, func(topic string, payload any) {
		calls = append(calls, "A")
		if err := bus.Unsubscribe("t", "B"); err != nil {
			t.Errorf("退订失败: %v", err)
		}
	})
	mustSubscribe(t, bus, "t", "B", 5, func(topic string, payload any) {
		calls = append(calls, "B")
	})
	mustSubscribe(t, bus, "t", "C", 1, func(topic string, payload any) {
		calls = append(calls, "C")
	})

	if _, err := bus.Publish("t", nil); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	want := []string{"A", "C"}
	t.Logf("输入: 订阅 t:[A(9),B(5),C(1)]；A 内退订 B；发布 t")
	t.Logf("输出: 调用序列=%v", calls)
	t.Logf("判定依据: 快照含 B，但轮到 B 时它已被退订，应跳过，得到 [A C]")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, want)
	}
}

// TestStopPropagation 验证停止传播只终止当前事件的后续处理器，
// 不影响已入队的其他事件；并验证无派发时调用被拒绝。
func TestStopPropagation(t *testing.T) {
	bus := NewBus(16)
	if err := bus.StopPropagation(); !errors.Is(err, ErrNotDispatching) {
		t.Fatalf("无派发时停止传播: err = %v, 期望 ErrNotDispatching", err)
	}

	var calls []string
	mustSubscribe(t, bus, "s", "A", 9, func(topic string, payload any) {
		calls = append(calls, "A")
		if _, err := bus.Publish("k", "queued"); err != nil {
			t.Errorf("重入发布失败: %v", err)
		}
		if err := bus.StopPropagation(); err != nil {
			t.Errorf("派发中停止传播失败: %v", err)
		}
	})
	mustSubscribe(t, bus, "s", "B", 1, func(topic string, payload any) {
		calls = append(calls, "B")
	})
	mustSubscribe(t, bus, "k", "K", 1, func(topic string, payload any) {
		calls = append(calls, "K")
	})

	res, err := bus.Publish("s", "first")
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	want := []string{"A", "K"}
	t.Logf("输入: 订阅 s:[A(9),B(1)] k:[K(1)]；A 内重入发布 k 并停止传播；发布 s")
	t.Logf("输出: 调用序列=%v, 结果=%+v", calls, res)
	t.Logf("判定依据: 停止传播跳过当前事件的 B，但已入队的 k 事件仍正常派发给 K，即 [A K]，共派发 2 个事件")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, 期望 2", res.Count)
	}
}

// TestPanicIsolated 验证处理器 panic 被捕获并记入错误记录，
// 不影响后续处理器与后续事件。
func TestPanicIsolated(t *testing.T) {
	bus := NewBus(16)
	var calls []string
	mustSubscribe(t, bus, "p", "A", 9, func(topic string, payload any) {
		panic("boom")
	})
	mustSubscribe(t, bus, "p", "B", 1, func(topic string, payload any) {
		calls = append(calls, "B")
		if _, err := bus.Publish("q", nil); err != nil {
			t.Errorf("重入发布失败: %v", err)
		}
	})
	mustSubscribe(t, bus, "q", "Q", 1, func(topic string, payload any) {
		calls = append(calls, "Q")
	})

	if _, err := bus.Publish("p", nil); err != nil {
		t.Fatalf("第一次发布失败: %v", err)
	}
	if _, err := bus.Publish("p", nil); err != nil {
		t.Fatalf("第二次发布失败: %v", err)
	}
	wantCalls := []string{"B", "Q", "B", "Q"}
	errs := bus.Errors()
	t.Logf("输入: 订阅 p:[A(9)会panic,B(1)内重入发布q] q:[Q(1)]；连续发布两次 p")
	t.Logf("输出: 调用序列=%v, 错误记录=%+v", calls, errs)
	t.Logf("判定依据: A 的 panic 被隔离，B、Q 照常执行；每次 panic 记录 (A, 事件序号)，两次事件序号为 1、3")
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, wantCalls)
	}
	if len(errs) != 2 ||
		errs[0].HandlerID != "A" || errs[0].EventSeq != 1 || errs[0].Panic != "boom" ||
		errs[1].HandlerID != "A" || errs[1].EventSeq != 3 || errs[1].Panic != "boom" {
		t.Fatalf("错误记录 = %+v, 期望 [(A,1,boom) (A,3,boom)]", errs)
	}
}

// TestQueueFullRejected 验证待派发队列恰满（达到上限 Q）时重入发布被整体
// 拒绝，且不消耗事件序号、不改变队列。
func TestQueueFullRejected(t *testing.T) {
	bus := NewBus(2) // Q = 2
	var calls []string
	var pubErrs []error
	mustSubscribe(t, bus, "start", "H", 1, func(topic string, payload any) {
		calls = append(calls, "H")
		for i := 0; i < 3; i++ {
			_, err := bus.Publish("e", i)
			pubErrs = append(pubErrs, err)
		}
	})
	mustSubscribe(t, bus, "e", "E", 1, func(topic string, payload any) {
		calls = append(calls, "E")
	})

	res, err := bus.Publish("start", nil)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	t.Logf("输入: Q=2；H 内连续重入发布 3 个 e 事件；发布 start")
	t.Logf("输出: 调用序列=%v, 重入发布错误=%v, 结果=%+v", calls, pubErrs, res)
	t.Logf("判定依据: 前两个重入发布入队(队列长度 1、2)，第三个时队列恰满(2>=Q)被拒绝；拒绝不消耗事件序号")
	if !reflect.DeepEqual(calls, []string{"H", "E", "E"}) {
		t.Fatalf("调用序列 = %v, 期望 [H E E]", calls)
	}
	if pubErrs[0] != nil || pubErrs[1] != nil || !errors.Is(pubErrs[2], ErrQueueFull) {
		t.Fatalf("重入发布错误 = %v, 期望 [nil nil ErrQueueFull]", pubErrs)
	}
	if res.Count != 3 {
		t.Fatalf("Count = %d, 期望 3", res.Count)
	}
	// 被拒绝的发布不得消耗事件序号：下一个发布应取序号 4（1=start, 2/3=入队的 e）。
	next, err := bus.Publish("e", "after")
	if err != nil {
		t.Fatalf("后续发布失败: %v", err)
	}
	if next.Seq != 4 {
		t.Fatalf("后续发布 Seq = %d, 期望 4（被拒绝的发布不得消耗序号）", next.Seq)
	}
	t.Logf("输出: 队列排空后下一次发布 Seq=%d，证明被拒绝的发布未消耗序号", next.Seq)
}

// TestPriorityAndSubscriptionOrder 验证同主题按优先级降序、同优先级按订阅
// 先后升序调用。
func TestPriorityAndSubscriptionOrder(t *testing.T) {
	bus := NewBus(16)
	var calls []string
	record := func(id string) Handler {
		return func(string, any) { calls = append(calls, id) }
	}
	mustSubscribe(t, bus, "t", "X", 5, record("X"))
	mustSubscribe(t, bus, "t", "Y", 5, record("Y"))
	mustSubscribe(t, bus, "t", "Z", 9, record("Z"))
	mustSubscribe(t, bus, "t", "W", 1, record("W"))

	if _, err := bus.Publish("t", nil); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	want := []string{"Z", "X", "Y", "W"}
	t.Logf("输入: 依次订阅 X(5),Y(5),Z(9),W(1)；发布 t")
	t.Logf("输出: 调用序列=%v", calls)
	t.Logf("判定依据: 优先级降序 Z(9)>{X,Y}(5)>W(1)；同优先级的 X,Y 按订阅先后，即 [Z X Y W]")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, want)
	}
}

// TestRejections 验证各类整体拒绝给出可区分原因，且不改变订阅表、队列与
// 事件序号计数。
func TestRejections(t *testing.T) {
	bus := NewBus(4)
	noop := func(string, any) {}

	if err := bus.Subscribe("", "a", 1, noop); !errors.Is(err, ErrEmptyTopic) {
		t.Fatalf("空主题订阅: err = %v, 期望 ErrEmptyTopic", err)
	}
	if _, err := bus.Publish("", nil); !errors.Is(err, ErrEmptyTopic) {
		t.Fatalf("空主题发布: err = %v, 期望 ErrEmptyTopic", err)
	}
	if err := bus.Subscribe("t", "a", 1, noop); err != nil {
		t.Fatalf("首次订阅失败: %v", err)
	}
	if err := bus.Subscribe("t", "a", 9, noop); !errors.Is(err, ErrDuplicateSubscription) {
		t.Fatalf("重复订阅: err = %v, 期望 ErrDuplicateSubscription", err)
	}
	if err := bus.Unsubscribe("t", "missing"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("退订不存在: err = %v, 期望 ErrSubscriptionNotFound", err)
	}
	if err := bus.Unsubscribe("", "a"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("空主题退订: err = %v, 期望 ErrSubscriptionNotFound", err)
	}

	subs := bus.Subscriptions("t")
	t.Logf("输入: 空主题订阅/发布、重复订阅 t/a、退订 t/missing、空主题退订")
	t.Logf("输出: 订阅表=%+v, 待派发队列长度=%d", subs, bus.Pending())
	t.Logf("判定依据: 所有拒绝不得改变订阅表与队列；重复订阅被拒绝后 t/a 的优先级仍为 1")
	if len(subs) != 1 || subs[0] != (SubscriptionInfo{Topic: "t", ID: "a", Priority: 1}) {
		t.Fatalf("订阅表 = %+v, 期望仅 [{t a 1}]", subs)
	}
	if p := bus.Pending(); p != 0 {
		t.Fatalf("待派发队列长度 = %d, 期望 0", p)
	}
	res, err := bus.Publish("t", nil)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	if res.Seq != 1 {
		t.Fatalf("Seq = %d, 期望 1（被拒绝的操作不得消耗事件序号）", res.Seq)
	}
	t.Logf("输出: 全部拒绝后首次成功发布 Seq=%d，证明序号计数未被拒绝操作消耗", res.Seq)
}

// ---- 朴素逐步模拟器：按规格逐条直译，用于与 Bus 对照 ----

// behavior 是模拟器与真实总线共用的处理器行为定义。
type behavior func(e env, topic string, payload any)

// env 抽象订阅/退订/发布/停止传播/记录日志，真实总线与朴素模拟器各实现一份。
type env interface {
	sub(topic, id string, priority int, b behavior) error
	unsub(topic, id string) error
	pub(topic string, payload any) (PublishResult, error)
	stop() error
	log(entry string)
}

// busEnv 把真实 Bus 适配为 env。
type busEnv struct {
	bus   *Bus
	calls *[]string
}

func (e busEnv) sub(topic, id string, priority int, b behavior) error {
	return e.bus.Subscribe(topic, id, priority, func(t string, p any) { b(e, t, p) })
}
func (e busEnv) unsub(topic, id string) error { return e.bus.Unsubscribe(topic, id) }
func (e busEnv) pub(topic string, payload any) (PublishResult, error) {
	return e.bus.Publish(topic, payload)
}
func (e busEnv) stop() error      { return e.bus.StopPropagation() }
func (e busEnv) log(entry string) { *e.calls = append(*e.calls, entry) }

type modelSub struct {
	id       string
	priority int
	fn       behavior
}

type modelEvent struct {
	seq     int
	topic   string
	payload any
}

// model 是规格的单线程朴素直译：全局 FIFO、派发时快照、退订跳过、
// 停止传播、panic 记录、队列上限。
type model struct {
	subs        map[string][]modelSub
	queue       []modelEvent
	queueCap    int
	seq         int
	dispatching bool
	stopFlag    bool
	errs        []ErrorRecord
	calls       *[]string
}

func newModel(queueCap int, calls *[]string) *model {
	return &model{subs: map[string][]modelSub{}, queueCap: queueCap, calls: calls}
}

func (m *model) sub(topic, id string, priority int, b behavior) error {
	if topic == "" {
		return ErrEmptyTopic
	}
	list := m.subs[topic]
	for _, s := range list {
		if s.id == id {
			return ErrDuplicateSubscription
		}
	}
	pos := len(list)
	for i, s := range list {
		if s.priority < priority {
			pos = i
			break
		}
	}
	list = append(list, modelSub{})
	copy(list[pos+1:], list[pos:])
	list[pos] = modelSub{id: id, priority: priority, fn: b}
	m.subs[topic] = list
	return nil
}

func (m *model) unsub(topic, id string) error {
	list := m.subs[topic]
	for i, s := range list {
		if s.id == id {
			m.subs[topic] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return ErrSubscriptionNotFound
}

func (m *model) pub(topic string, payload any) (PublishResult, error) {
	if topic == "" {
		return PublishResult{}, ErrEmptyTopic
	}
	if m.dispatching {
		if len(m.queue) >= m.queueCap {
			return PublishResult{}, ErrQueueFull
		}
		m.seq++
		m.queue = append(m.queue, modelEvent{seq: m.seq, topic: topic, payload: payload})
		return PublishResult{Seq: m.seq}, nil
	}
	m.dispatching = true
	m.seq++
	seq := m.seq
	m.queue = append(m.queue, modelEvent{seq: seq, topic: topic, payload: payload})
	count := 0
	for len(m.queue) > 0 {
		ev := m.queue[0]
		m.queue = m.queue[1:]
		snapshot := append([]modelSub(nil), m.subs[ev.topic]...)
		m.stopFlag = false
		count++
		for _, s := range snapshot {
			if m.stopFlag {
				break
			}
			if !modelHas(m.subs[ev.topic], s.id) {
				continue
			}
			m.call(ev, s)
		}
	}
	m.dispatching = false
	return PublishResult{Seq: seq, Dispatched: true, Count: count}, nil
}

func modelHas(list []modelSub, id string) bool {
	for _, s := range list {
		if s.id == id {
			return true
		}
	}
	return false
}

func (m *model) call(ev modelEvent, s modelSub) {
	defer func() {
		if r := recover(); r != nil {
			m.errs = append(m.errs, ErrorRecord{HandlerID: s.id, EventSeq: ev.seq, Panic: r})
		}
	}()
	s.fn(modelEnv{m}, ev.topic, ev.payload)
}

func (m *model) stop() error {
	if !m.dispatching {
		return ErrNotDispatching
	}
	m.stopFlag = true
	return nil
}

// modelEnv 把朴素模拟器适配为 env。
type modelEnv struct{ m *model }

func (e modelEnv) sub(topic, id string, priority int, b behavior) error {
	return e.m.sub(topic, id, priority, b)
}
func (e modelEnv) unsub(topic, id string) error { return e.m.unsub(topic, id) }
func (e modelEnv) pub(topic string, payload any) (PublishResult, error) {
	return e.m.pub(topic, payload)
}
func (e modelEnv) stop() error      { return e.m.stop() }
func (e modelEnv) log(entry string) { *e.m.calls = append(*e.m.calls, entry) }

// testBehaviors 返回对照脚本使用的处理器行为表，同时驱动真实总线与模拟器。
func testBehaviors() map[string]behavior {
	b := map[string]behavior{}
	b["C"] = func(e env, _ string, _ any) {
		e.log("C")
		_ = e.unsub("t", "B") // 快照后、轮到 B 前退订它
	}
	b["A"] = func(e env, _ string, _ any) {
		e.log("A")
		_, _ = e.pub("u", "fromA") // 重入发布，应排队而非嵌套
	}
	b["B"] = func(e env, _ string, _ any) { e.log("B") }
	b["D"] = func(e env, _ string, _ any) {
		e.log("D")
		panic("boom-D")
	}
	b["U3"] = func(e env, _ string, _ any) {
		e.log("U3")
		_ = e.sub("u", "U4", 7, b["U4"]) // 对当前事件不可见；重复时忽略
	}
	b["U4"] = func(e env, _ string, _ any) { e.log("U4") }
	b["U1"] = func(e env, _ string, _ any) {
		e.log("U1")
		_ = e.stop() // 停止传播：跳过 U2，不影响其他事件
	}
	b["U2"] = func(e env, _ string, _ any) { e.log("U2") }
	b["F1"] = func(e env, _ string, _ any) {
		e.log("F1")
		for i := 0; i < 3; i++ { // Q=2，第三次应因队列恰满被拒绝
			if _, err := e.pub("g", i); errors.Is(err, ErrQueueFull) {
				e.log("F1:queue-full")
			}
		}
	}
	b["G"] = func(e env, _ string, _ any) { e.log("G") }
	return b
}

type op struct {
	desc     string
	kind     string // "sub" | "unsub" | "pub" | "stop"
	topic    string
	id       string
	priority int
	beh      string
	payload  any
}

func applyOp(e env, behaviors map[string]behavior, o op) string {
	switch o.kind {
	case "sub":
		return fmt.Sprintf("err=%v", e.sub(o.topic, o.id, o.priority, behaviors[o.beh]))
	case "unsub":
		return fmt.Sprintf("err=%v", e.unsub(o.topic, o.id))
	case "pub":
		res, err := e.pub(o.topic, o.payload)
		return fmt.Sprintf("res=%+v err=%v", res, err)
	case "stop":
		return fmt.Sprintf("err=%v", e.stop())
	}
	return ""
}

// TestCompareWithNaiveModel 用同一份操作脚本分别驱动真实总线与朴素“朴素逐步
// 模拟”，比对处理器调用序列、错误记录与每一步的返回结果。
func TestCompareWithNaiveModel(t *testing.T) {
	behaviors := testBehaviors()
	script := []op{
		{desc: "订阅 t/C(9)", kind: "sub", topic: "t", id: "C", priority: 9, beh: "C"},
		{desc: "订阅 t/A(5)", kind: "sub", topic: "t", id: "A", priority: 5, beh: "A"},
		{desc: "订阅 t/B(5)", kind: "sub", topic: "t", id: "B", priority: 5, beh: "B"},
		{desc: "订阅 t/D(1)", kind: "sub", topic: "t", id: "D", priority: 1, beh: "D"},
		{desc: "订阅 u/U3(7)", kind: "sub", topic: "u", id: "U3", priority: 7, beh: "U3"},
		{desc: "订阅 u/U1(1)", kind: "sub", topic: "u", id: "U1", priority: 1, beh: "U1"},
		{desc: "订阅 u/U2(1)", kind: "sub", topic: "u", id: "U2", priority: 1, beh: "U2"},
		{desc: "订阅 f/F1(1)", kind: "sub", topic: "f", id: "F1", priority: 1, beh: "F1"},
		{desc: "订阅 g/G(1)", kind: "sub", topic: "g", id: "G", priority: 1, beh: "G"},
		{desc: "发布 t(first)", kind: "pub", topic: "t", payload: "first"},
		{desc: "发布 u(second)", kind: "pub", topic: "u", payload: "second"},
		{desc: "发布 f(go)", kind: "pub", topic: "f", payload: "go"},
		{desc: "发布 g(after)", kind: "pub", topic: "g", payload: "after"},
		{desc: "订阅空主题", kind: "sub", topic: "", id: "X", priority: 1, beh: "B"},
		{desc: "重复订阅 t/A", kind: "sub", topic: "t", id: "A", priority: 3, beh: "A"},
		{desc: "退订不存在 t/NOPE", kind: "unsub", topic: "t", id: "NOPE"},
		{desc: "无派发时停止传播", kind: "stop"},
		{desc: "发布空主题", kind: "pub", topic: "", payload: nil},
	}

	var busCalls, modelCalls []string
	bus := NewBus(2)
	be := busEnv{bus: bus, calls: &busCalls}
	me := modelEnv{m: newModel(2, &modelCalls)}

	var busOutcomes, modelOutcomes []string
	for _, o := range script {
		busOutcomes = append(busOutcomes, applyOp(be, behaviors, o))
		modelOutcomes = append(modelOutcomes, applyOp(me, behaviors, o))
	}

	var scriptDescs []string
	for _, o := range script {
		scriptDescs = append(scriptDescs, o.desc)
	}
	t.Logf("输入脚本: %s", strings.Join(scriptDescs, " | "))
	for i, o := range script {
		t.Logf("步骤 %02d [%s] 总线: %s | 模拟: %s", i+1, o.desc, busOutcomes[i], modelOutcomes[i])
	}
	t.Logf("输出: 总线调用序列=%v", busCalls)
	t.Logf("输出: 模拟调用序列=%v", modelCalls)
	t.Logf("输出: 总线错误记录=%+v", bus.Errors())
	t.Logf("输出: 模拟错误记录=%+v", me.m.errs)
	t.Logf("判定依据: 同一脚本下，逐步返回、处理器调用序列、错误记录三者必须与朴素模拟完全一致")

	if !reflect.DeepEqual(busOutcomes, modelOutcomes) {
		t.Fatalf("逐步返回不一致:\n总线: %v\n模拟: %v", busOutcomes, modelOutcomes)
	}
	if !reflect.DeepEqual(busCalls, modelCalls) {
		t.Fatalf("调用序列不一致:\n总线: %v\n模拟: %v", busCalls, modelCalls)
	}
	if !reflect.DeepEqual(bus.Errors(), me.m.errs) {
		t.Fatalf("错误记录不一致:\n总线: %+v\n模拟: %+v", bus.Errors(), me.m.errs)
	}
	// 锚定字面期望，防止总线与模拟同时偏离规格。
	wantCalls := []string{"C", "A", "D", "U3", "U1", "U3", "U4", "U1", "F1", "F1:queue-full", "G", "G", "G"}
	if !reflect.DeepEqual(busCalls, wantCalls) {
		t.Fatalf("调用序列 = %v, 期望 %v", busCalls, wantCalls)
	}
	wantErrs := []ErrorRecord{{HandlerID: "D", EventSeq: 1, Panic: "boom-D"}}
	if !reflect.DeepEqual(bus.Errors(), wantErrs) {
		t.Fatalf("错误记录 = %+v, 期望 %+v", bus.Errors(), wantErrs)
	}
}

// TestDeterministicReplay 验证相同的单协程调用序列重放得到完全相同的
// 处理器调用序列与错误记录。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]string, []ErrorRecord) {
		behaviors := testBehaviors()
		var calls []string
		bus := NewBus(2)
		e := busEnv{bus: bus, calls: &calls}
		for _, o := range []op{
			{kind: "sub", topic: "t", id: "C", priority: 9, beh: "C"},
			{kind: "sub", topic: "t", id: "A", priority: 5, beh: "A"},
			{kind: "sub", topic: "t", id: "B", priority: 5, beh: "B"},
			{kind: "sub", topic: "t", id: "D", priority: 1, beh: "D"},
			{kind: "sub", topic: "u", id: "U3", priority: 7, beh: "U3"},
			{kind: "sub", topic: "u", id: "U1", priority: 1, beh: "U1"},
			{kind: "sub", topic: "u", id: "U2", priority: 1, beh: "U2"},
			{kind: "pub", topic: "t", payload: "first"},
			{kind: "pub", topic: "u", payload: "second"},
		} {
			applyOp(e, behaviors, o)
		}
		return calls, bus.Errors()
	}
	calls1, errs1 := run()
	calls2, errs2 := run()
	t.Logf("输入: 同一固定脚本执行两次")
	t.Logf("输出: 第一次调用序列=%v 错误记录=%+v", calls1, errs1)
	t.Logf("输出: 第二次调用序列=%v 错误记录=%+v", calls2, errs2)
	t.Logf("判定依据: 两次重放的调用序列与错误记录必须完全相同")
	if !reflect.DeepEqual(calls1, calls2) || !reflect.DeepEqual(errs1, errs2) {
		t.Fatalf("重放不一致: (%v, %+v) vs (%v, %+v)", calls1, errs1, calls2, errs2)
	}
}

// TestConcurrentPublish 验证并发发布下的安全性：每个被接受的事件恰好派发
// 一次、处理器永不并发执行（任何时刻最多一个协程在派发）、队列最终排空。
func TestConcurrentPublish(t *testing.T) {
	bus := NewBus(64)
	var handled, repubbed, inFlight, violations, accepted int64
	mustSubscribe(t, bus, "c", "counter", 1, func(_ string, _ any) {
		if cur := atomic.AddInt64(&inFlight, 1); cur > 1 {
			atomic.AddInt64(&violations, 1)
		}
		atomic.AddInt64(&handled, 1)
		atomic.AddInt64(&inFlight, -1)
	})
	mustSubscribe(t, bus, "c", "repub", 0, func(_ string, payload any) {
		atomic.AddInt64(&repubbed, 1)
		if n, ok := payload.(int); ok && n > 0 {
			if _, err := bus.Publish("c", n-1); err == nil {
				atomic.AddInt64(&accepted, 1)
			}
		}
	})

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := bus.Publish("c", 3); err == nil {
					atomic.AddInt64(&accepted, 1)
				}
			}
		}()
	}
	wg.Wait()

	want := atomic.LoadInt64(&accepted)
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt64(&handled) != want {
		if time.Now().After(deadline) {
			t.Fatalf("超时: handled=%d, 期望=%d (accepted=%d)", handled, want, accepted)
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("输入: 8 协程 × 50 次并发发布(载荷3)，处理器内按载荷递减重入发布")
	t.Logf("输出: 接受事件=%d, counter调用=%d, repub调用=%d, 并发派发违规=%d, 剩余队列=%d",
		accepted, handled, repubbed, violations, bus.Pending())
	t.Logf("判定依据: 每个被接受事件恰好派发一次且两个处理器各被调用一次(counter==repub==accepted)；处理器互不并发；队列最终排空")
	if r := atomic.LoadInt64(&repubbed); r != want {
		t.Fatalf("repub 调用 = %d, 期望 %d", r, want)
	}
	if v := atomic.LoadInt64(&violations); v != 0 {
		t.Fatalf("检测到 %d 次并发派发，违反“任何时刻最多一个协程在派发”", v)
	}
	if p := bus.Pending(); p != 0 {
		t.Fatalf("队列未排空: %d", p)
	}
}

// TestNewSubscriptionVisibility 验证派发中新增的订阅对当前事件不可见、
// 对其后开始派发的事件可见。
func TestNewSubscriptionVisibility(t *testing.T) {
	bus := NewBus(16)
	var calls []string
	mustSubscribe(t, bus, "t", "A", 9, func(topic string, payload any) {
		calls = append(calls, "A")
		err := bus.Subscribe("t", "N", 7, func(string, any) {
			calls = append(calls, "N")
		})
		if err != nil && !errors.Is(err, ErrDuplicateSubscription) {
			t.Errorf("新增订阅失败: %v", err)
		}
	})
	mustSubscribe(t, bus, "t", "B", 1, func(topic string, payload any) {
		calls = append(calls, "B")
	})

	if _, err := bus.Publish("t", "first"); err != nil {
		t.Fatalf("第一次发布失败: %v", err)
	}
	if _, err := bus.Publish("t", "second"); err != nil {
		t.Fatalf("第二次发布失败: %v", err)
	}
	want := []string{"A", "B", "A", "N", "B"}
	t.Logf("输入: 订阅 t:[A(9),B(1)]；A 内新增订阅 N(7)；连续发布两次 t")
	t.Logf("输出: 调用序列=%v", calls)
	t.Logf("判定依据: 第一次事件快照不含 N，得 [A B]；第二次事件快照含 N(7) 且按优先级排在 B(1) 前，得 [A N B]")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("调用序列 = %v, 期望 %v", calls, want)
	}
}
