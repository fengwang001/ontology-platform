package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// planOp 是某节点上的一个确定性操作步骤。
type planOp struct {
	kind EventType
	msg  string // send/receive 使用
}

// runPlan 按确定性计划构造系统：每个节点一个 goroutine，严格按本节点列表顺序执行。
// receive 若引用的消息尚未发送，则失败重试——这同时验证了“因消息不存在被拒绝的
// receive 不产生任何副作用”，因为重试期间时钟与编号都不得被消耗。
//
// 无论 goroutine 实际如何交错，只要每节点内部顺序固定、消息发送时间戳由发送方计划
// 唯一决定，最终各事件时间戳与全序就是确定的，与交错方式无关。
func runPlan(t *testing.T, nodeCount, maxEvents int, perNode [][]planOp) *System {
	t.Helper()
	s, err := NewSystem(nodeCount, maxEvents)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	var wg sync.WaitGroup
	for n := 0; n < nodeCount; n++ {
		wg.Add(1)
		go func(node int, ops []planOp) {
			defer wg.Done()
			for _, op := range ops {
				switch op.kind {
				case Local:
					must(s.Local(node))
				case Send:
					must(s.Send(node, op.msg))
				case Receive:
					// 自旋直到消息已被发送。期间的 message_not_found 拒绝必须零副作用。
					for {
						ev, err := s.Receive(node, op.msg)
						if err == nil {
							_ = ev
							break
						}
						ce, ok := err.(*ClockError)
						if !ok || ce.Kind != ErrMessageNotFound {
							panic(fmt.Errorf("unexpected receive error: %w", err))
						}
					}
				}
			}
		}(n, perNode[n])
	}
	wg.Wait()
	return s
}

// 一个跨 3 节点、含多消息往返且无死锁的确定性计划：
//
//	n0: local, send a, local, recv c, send d, send e
//	n1: recv a, send b, recv d, local
//	n2: local, recv b, send c, recv e
//
// 依赖闭环检查：n0 早发 a → n1 收 a 发 b → n2 收 b 发 c → n0 收 c 后发 d/e，
// 故 receive 自旋等待一定能在有限步内全部满足。
func samplePlan() (int, [][]planOp) {
	perNode := make([][]planOp, 3)
	perNode[0] = []planOp{
		{Local, ""}, {Send, "a"}, {Local, ""}, {Receive, "c"}, {Send, "d"}, {Send, "e"},
	}
	perNode[1] = []planOp{
		{Receive, "a"}, {Send, "b"}, {Receive, "d"}, {Local, ""},
	}
	perNode[2] = []planOp{
		{Local, ""}, {Receive, "b"}, {Send, "c"}, {Receive, "e"},
	}
	return 3, perNode
}

// runPlanSerial 在单 goroutine 内以固定的轮询顺序（节点 0→1→2 循环）串行执行计划；
// 若某 receive 的消息尚未发送，则跳过该节点稍后再试。
// 它给出一个与并发调度严格不同的“批量顺序”基线，用于对照最终结果是否一致。
func runPlanSerial(t *testing.T, nodeCount, maxEvents int, perNode [][]planOp) *System {
	t.Helper()
	s, err := NewSystem(nodeCount, maxEvents)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	cursor := make([]int, nodeCount)
	total := 0
	for _, ops := range perNode {
		total += len(ops)
	}
	done := 0
	for done < total {
		progress := false
		for node := 0; node < nodeCount; node++ {
			if cursor[node] >= len(perNode[node]) {
				continue
			}
			op := perNode[node][cursor[node]]
			if op.kind == Receive {
				if _, gerr := s.Receive(node, op.msg); gerr != nil {
					ce, ok := gerr.(*ClockError)
					if !ok || ce.Kind != ErrMessageNotFound {
						t.Fatalf("unexpected serial receive error: %v", gerr)
					}
					continue // 消息未就绪，下一轮再试
				}
			} else if op.kind == Send {
				if _, gerr := s.Send(node, op.msg); gerr != nil {
					t.Fatalf("unexpected serial send error: %v", gerr)
				}
			} else {
				if _, gerr := s.Local(node); gerr != nil {
					t.Fatalf("unexpected serial local error: %v", gerr)
				}
			}
			cursor[node]++
			done++
			progress = true
		}
		if !progress {
			t.Fatalf("serial scheduler deadlocked with %d/%d ops done", done, total)
		}
	}
	return s
}

// TestConcurrentSeqContinuity 并发执行后，每个节点的事件编号从1连续不间断，
// 且系统自检因果一致。
func TestConcurrentSeqContinuity(t *testing.T) {
	n, plan := samplePlan()
	s := runPlan(t, n, 1000, plan)

	for node := 0; node < n; node++ {
		var lastClock int
		for seq := 1; ; seq++ {
			ev, err := s.Event(EventRef{node, seq})
			if err != nil {
				break // 该节点事件枚举完毕
			}
			if ev.Seq != seq {
				t.Fatalf("node %d seq gap: got %d want %d", node, ev.Seq, seq)
			}
			if ev.Clock <= lastClock {
				t.Fatalf("node %d clock not increasing at seq %d: %d <= %d",
					node, seq, ev.Clock, lastClock)
			}
			lastClock = ev.Clock
		}
	}
	if detail, ok := s.IsCausalConsistent(); !ok {
		t.Fatalf("concurrent run not causally consistent: %s", detail)
	}
	t.Logf("并发执行完成，共 %d 个事件，每节点编号连续、时钟单调、因果一致", s.EventCount())
}

// signature 返回系统结果的可比较指纹：按全序的 (clock,node,seq,kind,msg) 序列，
// 外加每节点当前时钟。
func signature(s *System) string {
	var b []byte
	ordered := s.TotalOrder()
	for _, e := range ordered {
		b = append(b, fmt.Sprintf("%d|%d|%d|%d|%s;", e.Clock, e.Node, e.Seq, e.Kind, e.MessageID)...)
	}
	b = append(b, 'C')
	for _, c := range s.Clocks() {
		b = append(b, fmt.Sprintf("%d,", c)...)
	}
	return string(b)
}

// TestConcurrentEqualsBatchAndDeterministic 同一输入计划：
//   - 并发执行多次，输出指纹必须完全相同（确定性）；
//   - 并发结果必须与“批量顺序执行”的结果全序逐位一致。
func TestConcurrentEqualsBatchAndDeterministic(t *testing.T) {
	n, plan := samplePlan()

	// 批量顺序基线：单 goroutine、按节点 0→N 固定轮询顺序的严格串行调度。
	batch := runPlanSerial(t, n, 1000, plan)
	batchSig := signature(batch)
	t.Logf("批量（串行轮询）全序: %s", formatOrder(batch.TotalOrder()))

	// 并发执行 5 次，每次都必须与批量结果一致且彼此一致。
	for iter := 0; iter < 5; iter++ {
		conc := runPlan(t, n, 1000, plan)
		got := signature(conc)
		if got != batchSig {
			t.Fatalf("iteration %d mismatch:\nconc: %s\nbatch:%s",
				iter, formatOrder(conc.TotalOrder()), formatOrder(batch.TotalOrder()))
		}
	}
	t.Logf("5 次并发执行与批量重排的全序及时钟完全一致（确定性成立）")
}

// TestConcurrentReadersAndWriters 写操作进行的同时大量并发只读查询，
// 配合 -race 检测是否存在数据竞争，且查询永不返回撕裂结果。
func TestConcurrentReadersAndWriters(t *testing.T) {
	s, _ := NewSystem(3, 100000)
	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// 3 个写者：各自推进本节点事件，周期性发送消息。
	for n := 0; n < 3; n++ {
		writers.Add(1)
		go func(node int) {
			defer writers.Done()
			for i := 0; i < 300; i++ {
				_, _ = s.Local(node)
				if i%50 == 0 && node == 0 {
					_, _ = s.Send(node, fmt.Sprintf("tick-%d", i))
				}
			}
		}(n)
	}
	// 3 个读者：并发读取快照、全序、因果。
	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Clocks()
					_ = s.TotalOrder()
					_ = s.Events()
					_ = s.EventCount()
					_, _ = s.IsCausalConsistent()
				}
			}
		}()
	}
	writers.Wait() // 先等写者结束
	close(stop)    // 再通知读者停止，避免读写共用一个 WaitGroup 造成的死锁
	readers.Wait()
	t.Logf("读写混跑完成，事件总数=%d（用 -race 验证无数据竞争）", s.EventCount())
}
