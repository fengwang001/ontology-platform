package closedts_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/closedts"
)

// manualNet 注入网络：只排队不投递，测试显式控制投递顺序与重复。
type manualNet struct {
	mu     sync.Mutex
	queues map[int][]closedts.Message
}

func newManualNet() *manualNet { return &manualNet{queues: map[int][]closedts.Message{}} }

func (n *manualNet) Send(to int, msg closedts.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.queues[to] = append(n.queues[to], msg)
}

// pending 返回某副本队列中的消息快照。
func (n *manualNet) pending(to int) []closedts.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]closedts.Message, len(n.queues[to]))
	copy(out, n.queues[to])
	return out
}

// deliverOne 投递队列中第 i 条消息（乱序由调用方选择 i 实现）。
func (n *manualNet) deliverOne(g *closedts.Group, to, i int) closedts.Message {
	n.mu.Lock()
	msg := n.queues[to][i]
	n.queues[to] = append(n.queues[to][:i], n.queues[to][i+1:]...)
	n.mu.Unlock()
	g.Replica(to).Receive(msg)
	return msg
}

// deliverAll 按队列顺序投递全部消息。
func (n *manualNet) deliverAll(g *closedts.Group, to int) {
	for len(n.pending(to)) > 0 {
		n.deliverOne(g, to, 0)
	}
}

// redeliver 重复投递同一条消息（模拟网络重复）。
func redeliver(g *closedts.Group, to int, msg closedts.Message) {
	g.Replica(to).Receive(msg)
}

// TestInflightWriteBlocksClosed 在途写阻止闭合时间戳越过它。
func TestInflightWriteBlocksClosed(t *testing.T) {
	clock := closedts.NewManualClock(1000)
	net := newManualNet()
	g, err := closedts.NewGroup(clock, net, 100, 1)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	w, err := g.BeginWrite("k", "v1", 500)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	t.Logf("输入: clock=1000, lag=100, 在途写 ts=%d (seq=%d)", w.Timestamp(), w.Seq())

	p := g.Publish()
	t.Logf("输出: Publish -> (Closed=%d, MaxSeq=%d)", p.Closed, p.MaxSeq)
	t.Logf("判定依据: 候选=clock-lag=900, 但必须小于在途写 ts=500, 故钳制为 499")
	if p.Closed != 499 {
		t.Fatalf("在途写未阻止闭合: got Closed=%d, want 499", p.Closed)
	}

	w.Apply()
	p = g.Publish()
	t.Logf("输入: 写已应用, 再次 Publish")
	t.Logf("输出: Publish -> (Closed=%d, MaxSeq=%d)", p.Closed, p.MaxSeq)
	t.Logf("判定依据: 在途集合为空, 候选=900 不再受钳制")
	if p.Closed != 900 {
		t.Fatalf("写应用后闭合未推进: got Closed=%d, want 900", p.Closed)
	}
}

// TestWriteEqualToClosedRaised 期望时间戳恰等于已发布闭合时间戳 c 的写被抬高为 c+1。
func TestWriteEqualToClosedRaised(t *testing.T) {
	clock := closedts.NewManualClock(50)
	net := newManualNet()
	g, err := closedts.NewGroup(clock, net, 0, 1)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	p := g.Publish()
	t.Logf("输入: clock=50, lag=0 -> Publish")
	t.Logf("输出: Publish -> (Closed=%d, MaxSeq=%d)", p.Closed, p.MaxSeq)
	if p.Closed != 50 {
		t.Fatalf("got Closed=%d, want 50", p.Closed)
	}

	w, err := g.BeginWrite("k", "v", 50)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	t.Logf("输入: BeginWrite(ts=50), 已发布闭合 c=50")
	t.Logf("输出: 最终时间戳=%d", w.Timestamp())
	t.Logf("判定依据: 期望时间戳不大于 c 时抬高为 c+1=51, 保证大于提案前发布的全部闭合时间戳")
	if w.Timestamp() != 51 {
		t.Fatalf("写未被抬高: got ts=%d, want 51", w.Timestamp())
	}
	w.Apply()

	// 闭合时间戳单调不减：时钟倒退也不回退。
	clock.Advance(-20)
	p = g.Publish()
	t.Logf("输入: 时钟倒退到 30 后 Publish")
	t.Logf("输出: Publish -> (Closed=%d, MaxSeq=%d)", p.Closed, p.MaxSeq)
	t.Logf("判定依据: 候选 30 小于上次发布值 50, 保持上次值")
	if p.Closed != 50 {
		t.Fatalf("闭合时间戳回退: got Closed=%d, want 50", p.Closed)
	}
}

// TestFollowerServesWithEarlierPublish 从副本落后时改用较早发布仍能服务。
func TestFollowerServesWithEarlierPublish(t *testing.T) {
	clock := closedts.NewManualClock(0)
	net := newManualNet()
	g, err := closedts.NewGroup(clock, net, 0, 1)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}

	// seq1: ts=10, seq2: ts=20，随后发布 P1(Closed=25, MaxSeq=2)。
	mustWrite(t, g, "k", "a", 10)
	mustWrite(t, g, "k", "b", 20)
	clock.Advance(25)
	p1 := g.Publish()
	// seq3: ts=30，随后发布 P2(Closed=35, MaxSeq=3)。
	mustWrite(t, g, "k", "c", 30)
	clock.Advance(10)
	p2 := g.Publish()
	t.Logf("输入: 写 ts=10,20,30; P1=(Closed=%d,MaxSeq=%d), P2=(Closed=%d,MaxSeq=%d)",
		p1.Closed, p1.MaxSeq, p2.Closed, p2.MaxSeq)

	// 乱序投递：先 P2，再日志 1、2（日志 3 不投递），最后 P1。
	msgs := net.pending(1)
	var mP1, mP2, mE1, mE2 closedts.Message
	for _, m := range msgs {
		switch m := m.(type) {
		case closedts.Publish:
			if m.Closed == p1.Closed {
				mP1 = m
			} else {
				mP2 = m
			}
		case closedts.LogEntry:
			if m.Seq == 1 {
				mE1 = m
			} else if m.Seq == 2 {
				mE2 = m
			}
		}
	}
	r := g.Replica(1)
	r.Receive(mP2)
	r.Receive(mE2) // 乱序：seq2 先到，应缓冲等待 seq1
	r.Receive(mE1)
	redeliver(g, 1, mE1) // 重复投递安全
	r.Receive(mP1)
	t.Logf("从副本状态: AppliedSeq=%d (日志3未投递)", r.AppliedSeq())
	if r.AppliedSeq() != 2 {
		t.Fatalf("got AppliedSeq=%d, want 2", r.AppliedSeq())
	}

	// t=20：P2 要求 seq>=3 未满足，改用较早的 P1(Closed=25>=20, MaxSeq=2) 服务。
	v, ok, err := g.Read(1, "k", 20)
	t.Logf("输入: Read(replica=1, k, t=20)")
	t.Logf("输出: value=%q ok=%v err=%v", v, ok, err)
	t.Logf("判定依据: P1 满足 20<=25 且 AppliedSeq=2>=MaxSeq=2, 能服务必须服务")
	if err != nil || !ok || v != "b" {
		t.Fatalf("got (%q,%v,%v), want (\"b\",true,nil)", v, ok, err)
	}

	// t=30：没有发布能满足序号条件，但已有闭合>=30 的发布 -> 未追上。
	_, _, err = g.Read(1, "k", 30)
	t.Logf("输入: Read(replica=1, k, t=30)")
	t.Logf("输出: err=%v", err)
	t.Logf("判定依据: P2 闭合 35>=30 但 AppliedSeq=2<MaxSeq=3, 存在闭合不小于 t 的发布 -> 未追上")
	if !errors.Is(err, closedts.ErrNotCaughtUp) {
		t.Fatalf("got err=%v, want ErrNotCaughtUp", err)
	}

	// t=40：没有任何发布的闭合时间戳不小于 40 -> 未闭合。
	_, _, err = g.Read(1, "k", 40)
	t.Logf("输入: Read(replica=1, k, t=40)")
	t.Logf("输出: err=%v", err)
	t.Logf("判定依据: 已收到发布的最大闭合为 35 < 40 -> 未闭合")
	if !errors.Is(err, closedts.ErrNotClosed) {
		t.Fatalf("got err=%v, want ErrNotClosed", err)
	}

	// 补投日志 3 后 t=30 可服务，且与主副本一致。
	net.deliverAll(g, 1)
	v, ok, err = g.Read(1, "k", 30)
	t.Logf("输入: 补投日志3后 Read(replica=1, k, t=30)")
	t.Logf("输出: value=%q ok=%v err=%v", v, ok, err)
	t.Logf("判定依据: AppliedSeq=3>=P2.MaxSeq=3 且 30<=35, 就地服务")
	if err != nil || !ok || v != "c" {
		t.Fatalf("got (%q,%v,%v), want (\"c\",true,nil)", v, ok, err)
	}
}

func mustWrite(t *testing.T, g *closedts.Group, key, value string, ts int64) *closedts.Write {
	t.Helper()
	w, err := g.BeginWrite(key, value, ts)
	if err != nil {
		t.Fatalf("BeginWrite(%q,%q,%d): %v", key, value, ts, err)
	}
	w.Apply()
	return w
}

// TestValidation 负滞后、负时间戳、不存在的副本整体拒绝且可区分，且不改变状态。
func TestValidation(t *testing.T) {
	if _, err := closedts.NewGroup(closedts.NewManualClock(0), newManualNet(), -1, 1); !errors.Is(err, closedts.ErrNegativeLag) {
		t.Fatalf("负滞后: got %v, want ErrNegativeLag", err)
	}
	t.Logf("输入: NewGroup(lag=-1) -> 输出: ErrNegativeLag; 判定依据: 目标滞后为负整体拒绝")

	g, err := closedts.NewGroup(closedts.NewManualClock(10), newManualNet(), 0, 1)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	if _, err := g.BeginWrite("k", "v", -5); !errors.Is(err, closedts.ErrNegativeTimestamp) {
		t.Fatalf("负时间戳写: got %v, want ErrNegativeTimestamp", err)
	}
	t.Logf("输入: BeginWrite(ts=-5) -> 输出: ErrNegativeTimestamp; 判定依据: 时间戳为负整体拒绝")

	if _, _, err := g.Read(99, "k", 1); !errors.Is(err, closedts.ErrReplicaNotFound) {
		t.Fatalf("不存在的副本: got %v, want ErrReplicaNotFound", err)
	}
	t.Logf("输入: Read(replica=99) -> 输出: ErrReplicaNotFound; 判定依据: 指向不存在的副本整体拒绝")

	if _, _, err := g.Read(1, "k", -1); !errors.Is(err, closedts.ErrNegativeTimestamp) {
		t.Fatalf("负时间戳读: got %v, want ErrNegativeTimestamp", err)
	}
	t.Logf("输入: Read(replica=1, t=-1) -> 输出: ErrNegativeTimestamp; 判定依据: 时间戳为负整体拒绝")

	// 被拒绝的操作不得改变任何副本状态：下一个写仍应分配到 seq=1，闭合仍为 0。
	w, err := g.BeginWrite("k", "v", 7)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if w.Seq() != 1 || w.Timestamp() != 7 {
		t.Fatalf("状态被失败操作污染: seq=%d ts=%d, want seq=1 ts=7", w.Seq(), w.Timestamp())
	}
	if got := g.Closed(); got != 0 {
		t.Fatalf("闭合时间戳被失败操作改变: got %d, want 0", got)
	}
	if got := g.Replica(1).AppliedSeq(); got != 0 {
		t.Fatalf("从副本状态被失败操作改变: AppliedSeq=%d, want 0", got)
	}
	t.Logf("判定依据: 拒绝后下一写 seq=1、ts 未被抬高、Closed=0、AppliedSeq=0, 状态未被改变")
}

// TestRandomCrossCheck 随机操作序列下，从副本服务的读与主副本同一时间戳的读对拍。
func TestRandomCrossCheck(t *testing.T) {
	runRandomScenario(t, 20260930, true)
}

// TestDeterministicReplay 相同的操作、时钟与投递序列重放结果相同。
func TestDeterministicReplay(t *testing.T) {
	first := runRandomScenario(t, 42, false)
	second := runRandomScenario(t, 42, false)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatal("相同种子重放结果不同")
	}
	t.Logf("判定依据: 种子 42 的两次重放产生完全相同的 %d 条操作日志", len(first))
}

// runRandomScenario 以固定种子执行随机写/发布/乱序重复投递/读，返回操作日志。
func runRandomScenario(t *testing.T, seed int64, verbose bool) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	clock := closedts.NewManualClock(0)
	net := newManualNet()
	g, err := closedts.NewGroup(clock, net, 5, 1, 2)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	keys := []string{"a", "b", "c"}
	var inflight []*closedts.Write
	var lines []string
	record := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		lines = append(lines, line)
		if verbose {
			t.Log(line)
		}
	}

	for i := 0; i < 400; i++ {
		op := rng.Intn(100)
		switch {
		case op < 35: // 提案写（可积累在途）
			key := keys[rng.Intn(len(keys))]
			ts := int64(rng.Intn(120))
			closedBefore := g.Closed()
			w, err := g.BeginWrite(key, fmt.Sprintf("v%d", i), ts)
			if err != nil {
				t.Fatalf("BeginWrite: %v", err)
			}
			record("op=%d 提案写 key=%s 期望ts=%d -> seq=%d 最终ts=%d (提案前Closed=%d)",
				i, key, ts, w.Seq(), w.Timestamp(), closedBefore)
			if w.Timestamp() <= closedBefore {
				t.Fatalf("写最终时间戳 %d 不大于提案前闭合 %d", w.Timestamp(), closedBefore)
			}
			inflight = append(inflight, w)
		case op < 55: // 应用一个在途写
			if len(inflight) == 0 {
				continue
			}
			j := rng.Intn(len(inflight))
			w := inflight[j]
			inflight = append(inflight[:j], inflight[j+1:]...)
			w.Apply()
			record("op=%d 应用写 seq=%d ts=%d", i, w.Seq(), w.Timestamp())
		case op < 70: // 推进时钟并发布
			clock.Advance(int64(rng.Intn(10)))
			before := g.Closed()
			p := g.Publish()
			record("op=%d 发布 -> (Closed=%d, MaxSeq=%d) (发布前Closed=%d)", i, p.Closed, p.MaxSeq, before)
			if p.Closed < before {
				t.Fatalf("闭合时间戳回退: %d < %d", p.Closed, before)
			}
		case op < 90: // 乱序投递一条消息，偶尔重复投递
			id := 1 + rng.Intn(2)
			msgs := net.pending(id)
			if len(msgs) == 0 {
				continue
			}
			m := net.deliverOne(g, id, rng.Intn(len(msgs)))
			record("op=%d 投递 replica=%d msg=%v", i, id, m)
			if rng.Intn(4) == 0 {
				redeliver(g, id, m)
				record("op=%d 重复投递 replica=%d msg=%v", i, id, m)
			}
		default: // 随机从副本读并与主副本对拍
			id := 1 + rng.Intn(2)
			key := keys[rng.Intn(len(keys))]
			ts := int64(rng.Intn(120))
			v, ok, err := g.Read(id, key, ts)
			record("op=%d 读 replica=%d key=%s t=%d -> value=%q ok=%v err=%v", i, id, key, ts, v, ok, err)
			if err != nil {
				if !errors.Is(err, closedts.ErrNotClosed) && !errors.Is(err, closedts.ErrNotCaughtUp) {
					t.Fatalf("未知读错误: %v", err)
				}
				continue
			}
			pv, pok, perr := g.ReadPrimary(key, ts)
			if perr != nil {
				t.Fatalf("ReadPrimary: %v", perr)
			}
			record("op=%d 对拍 主副本 key=%s t=%d -> value=%q ok=%v", i, key, ts, pv, pok)
			if v != pv || ok != pok {
				t.Fatalf("从副本读 (%q,%v) 与主副本 (%q,%v) 不一致", v, ok, pv, pok)
			}
		}
	}

	// 收尾：应用在途写、推进时钟、发布并全部投递，之后 t<=Closed 的读必须服务且与主副本一致。
	for _, w := range inflight {
		w.Apply()
	}
	clock.Advance(1000)
	final := g.Publish()
	record("收尾发布 -> (Closed=%d, MaxSeq=%d)", final.Closed, final.MaxSeq)
	net.deliverAll(g, 1)
	net.deliverAll(g, 2)
	for _, id := range []int{1, 2} {
		for _, key := range keys {
			for ts := int64(0); ts <= final.Closed; ts += 7 {
				v, ok, err := g.Read(id, key, ts)
				if err != nil {
					t.Fatalf("收尾读 replica=%d key=%s t=%d 应能服务: %v", id, key, ts, err)
				}
				pv, pok, _ := g.ReadPrimary(key, ts)
				if v != pv || ok != pok {
					t.Fatalf("收尾对拍不一致 replica=%d key=%s t=%d: 从=(%q,%v) 主=(%q,%v)",
						id, key, ts, v, ok, pv, pok)
				}
			}
		}
	}
	record("收尾对拍通过: 两个从副本 t<=%d 的读均与主副本一致", final.Closed)
	return lines
}

// directNet 立即投递网络，用于并发冒烟。
type directNet struct {
	g *closedts.Group
}

func (n *directNet) Send(to int, msg closedts.Message) { n.g.Replica(to).Receive(msg) }

// TestConcurrent 写、发布、投递与读并发调用（配合 -race）。
func TestConcurrent(t *testing.T) {
	clock := closedts.NewManualClock(0)
	net := &directNet{}
	g, err := closedts.NewGroup(clock, net, 1, 1, 2)
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	net.g = g

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 200; i++ {
				switch rng.Intn(4) {
				case 0:
					w, err := g.BeginWrite("k", "v", int64(rng.Intn(50)))
					if err == nil {
						w.Apply()
					}
				case 1:
					clock.Advance(1)
					g.Publish()
				case 2:
					_, _, _ = g.Read(1+rng.Intn(2), "k", int64(rng.Intn(50)))
				default:
					_, _, _ = g.ReadPrimary("k", int64(rng.Intn(50)))
				}
			}
		}(worker)
	}
	wg.Wait()
	t.Logf("并发冒烟完成: Closed=%d", g.Closed())
}
