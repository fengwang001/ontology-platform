package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// testBuilders 注册测试用补偿计划：
//   - "credit": 对两个对象共三项副作用（a.balance+=n, b.balance-=n, a.count+=1）。
//   - "broken": 计划构造失败。
//   - "badop": 计划含未知操作。
func testBuilders() map[string]CompensationBuilder {
	return map[string]CompensationBuilder{
		"credit": func(evt ChangeEvent) ([]SideEffect, error) {
			n, _ := evt.Payload["amount"].(int)
			return []SideEffect{
				{ObjectID: "a", Op: OpAdd, Field: "balance", Value: n},
				{ObjectID: "b", Op: OpAdd, Field: "balance", Value: -n},
				{ObjectID: "a", Op: OpAdd, Field: "count", Value: 1},
			}, nil
		},
		"broken": func(evt ChangeEvent) ([]SideEffect, error) {
			return nil, errors.New("无法构造补偿计划")
		},
		"badop": func(evt ChangeEvent) ([]SideEffect, error) {
			return []SideEffect{{ObjectID: "a", Op: "explode", Field: "balance", Value: 1}}, nil
		},
	}
}

func newTestStorage() *Storage {
	st := NewStorage(fixedClock())
	st.Store.Put(Object{ID: "a", Fields: map[string]int{"balance": 0, "count": 0}})
	st.Store.Put(Object{ID: "b", Fields: map[string]int{"balance": 0}})
	return st
}

func creditEvent(eventID, actionID string, amount int) ChangeEvent {
	return ChangeEvent{
		EventID:           eventID,
		ActionExecutionID: actionID,
		Kind:              KindActionSucceeded,
		ActionType:        "credit",
		Payload:           map[string]any{"amount": amount},
	}
}

// tryConsume 消费消费事件；若注入故障导致 panic（模拟进程崩溃）则 crashed=true。
func tryConsume(c *Consumer, evt ChangeEvent) (res ConsumeResult, crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			crashed = true
		}
	}()
	res = c.Consume(evt)
	return
}

func field(t *testing.T, st *Storage, objID, name string) int {
	t.Helper()
	o, ok := st.Store.Get(objID)
	if !ok {
		t.Fatalf("对象 %q 不存在", objID)
	}
	return o.Fields[name]
}

// TestExactlyOnceOnRedelivery 重复投递同一事件，补偿恰好执行一次。
func TestExactlyOnceOnRedelivery(t *testing.T) {
	st := newTestStorage()
	c := NewConsumer(st, testBuilders())
	evt := creditEvent("evt-1", "act-1", 10)

	res := c.Consume(evt)
	if res.Outcome != OutcomeCompensated {
		t.Fatalf("首次消费应完成补偿，得到 %v", res.Outcome)
	}
	for i := 0; i < 5; i++ {
		res = c.Consume(evt)
		if res.Outcome != OutcomeDuplicateSkipped {
			t.Fatalf("第 %d 次重复投递应被跳过，得到 %v", i, res.Outcome)
		}
	}
	if got := field(t, st, "a", "balance"); got != 10 {
		t.Fatalf("a.balance 应为 10，得到 %d", got)
	}
	if got := field(t, st, "a", "count"); got != 1 {
		t.Fatalf("a.count 应为 1，得到 %d", got)
	}
	if got := field(t, st, "b", "balance"); got != -10 {
		t.Fatalf("b.balance 应为 -10，得到 %d", got)
	}
}

// TestIndependentEquivalentCalls 参数与效果等价但标识全新的两次调用，
// 必须各自触发独立触发一次补偿。
func TestIndependentEquivalentCalls(t *testing.T) {
	st := newTestStorage()
	c := NewConsumer(st, testBuilders())

	r1 := c.Consume(creditEvent("evt-1", "act-1", 10))
	r2 := c.Consume(creditEvent("evt-2", "act-2", 10))
	if r1.Outcome != OutcomeCompensated || r2.Outcome != OutcomeCompensated {
		t.Fatalf("两次独立调用都应完成补偿，得到 %v / %v", r1.Outcome, r2.Outcome)
	}
	if got := field(t, st, "a", "balance"); got != 20 {
		t.Fatalf("a.balance 应为 20，得到 %d", got)
	}
	if got := field(t, st, "a", "count"); got != 2 {
		t.Fatalf("a.count 应为 2，得到 %d", got)
	}
}

// TestResumeAfterCrash 注入故障：补偿部分生效后消费端崩溃重启，
// 重新消费同一事件必须续作剩余副作用，已生效部分不得重复施加。
func TestResumeAfterCrash(t *testing.T) {
	for crashAfter := 1; crashAfter <= 3; crashAfter++ {
		t.Run(fmt.Sprintf("crashAfter=%d", crashAfter), func(t *testing.T) {
			st := newTestStorage()
			evt := creditEvent("evt-1", "act-1", 10)

			crashing := NewConsumer(st, testBuilders(), WithCrashHook(
				func(id string, committed int) {
					if committed == crashAfter {
						panic("模拟进程崩溃")
					}
				}))
			if _, crashed := tryConsume(crashing, evt); !crashed {
				t.Fatalf("应发生注入崩溃")
			}

			// 崩溃后已提交的副作用保持生效。
			rec, ok := st.Journal.Get("act-1")
			if !ok || rec.State != StateInProgress || rec.CommittedCount() != crashAfter {
				t.Fatalf("崩溃后日志应为 InProgress 且已提交 %d 项，得到 %+v", crashAfter, rec)
			}

			// 用同一持久化状态构造新消费端（模拟重启），重新投递同一事件。
			restarted := NewConsumer(st, testBuilders())
			res := restarted.Consume(evt)
			if res.Outcome != OutcomeCompensated {
				t.Fatalf("重启后续作应完成补偿，得到 %v (%v)", res.Outcome, res.Err)
			}
			if got := field(t, st, "a", "balance"); got != 10 {
				t.Fatalf("a.balance 应为 10（不得重复施加），得到 %d", got)
			}
			if got := field(t, st, "a", "count"); got != 1 {
				t.Fatalf("a.count 应为 1，得到 %d", got)
			}
			if got := field(t, st, "b", "balance"); got != -10 {
				t.Fatalf("b.balance 应为 -10，得到 %d", got)
			}
			rec, _ = st.Journal.Get("act-1")
			if rec.State != StateCompleted || rec.CommittedCount() != 3 {
				t.Fatalf("续作后日志应为 Completed 且 3 项全部提交，得到 %+v", rec)
			}
		})
	}
}

// TestResumeCoexistsWithUnrelatedModification 补偿续作期间目标对象被
// 无关事件并发修改，续作不得一概拒绝，两者结果必须共存。
func TestResumeCoexistsWithUnrelatedModification(t *testing.T) {
	st := newTestStorage()
	builders := testBuilders()
	builders["unrelated"] = func(evt ChangeEvent) ([]SideEffect, error) {
		return []SideEffect{{ObjectID: "a", Op: OpSet, Field: "note", Value: 7}}, nil
	}
	evt := creditEvent("evt-1", "act-1", 10)

	crashing := NewConsumer(st, builders, WithCrashHook(
		func(id string, committed int) {
			if committed == 1 {
				panic("模拟进程崩溃")
			}
		}))
	tryConsume(crashing, evt)

	// 无关事件并发修改同一对象 a（在续作之前被串行化处理）。
	restarted := NewConsumer(st, builders)
	unrelated := ChangeEvent{
		EventID: "evt-u", ActionExecutionID: "act-u",
		Kind: KindActionSucceeded, ActionType: "unrelated",
	}
	if res := restarted.Consume(unrelated); res.Outcome != OutcomeCompensated {
		t.Fatalf("无关事件应正常补偿，得到 %v", res.Outcome)
	}

	// 续作原始补偿：不得因对象被修改过而拒绝。
	res := restarted.Consume(evt)
	if res.Outcome != OutcomeCompensated {
		t.Fatalf("续作应完成，得到 %v (%v)", res.Outcome, res.Err)
	}
	if got := field(t, st, "a", "balance"); got != 10 {
		t.Fatalf("a.balance 应为 10，得到 %d", got)
	}
	if got := field(t, st, "a", "note"); got != 7 {
		t.Fatalf("无关修改 a.note 应保留为 7，得到 %d", got)
	}
}

// TestUndoRules 撤销规则：补偿未提交任何副作用时被放弃；已提交部分
// 副作用后撤销到达，补偿继续完成。结果只取决于全局顺序，与时刻无关。
func TestUndoRules(t *testing.T) {
	t.Run("撤销先于补偿开始", func(t *testing.T) {
		st := newTestStorage()
		c := NewConsumer(st, testBuilders())
		undo := ChangeEvent{
			EventID: "evt-undo", ActionExecutionID: "act-undo",
			Kind:    KindActionReverted,
			Payload: map[string]any{"OriginalActionExecutionID": "act-1"},
		}
		if res := c.Consume(undo); res.Outcome != OutcomeAbandoned {
			t.Fatalf("撤销应放弃补偿，得到 %v", res.Outcome)
		}
		// 原始成功事件随后到达（乱序），不得再执行补偿。
		res := c.Consume(creditEvent("evt-1", "act-1", 10))
		if res.Outcome != OutcomeAbandoned {
			t.Fatalf("已放弃的补偿不得执行，得到 %v", res.Outcome)
		}
		if got := field(t, st, "a", "balance"); got != 0 {
			t.Fatalf("副作用不得生效，a.balance 应为 0，得到 %d", got)
		}
	})

	t.Run("撤销到达时补偿已部分生效", func(t *testing.T) {
		st := newTestStorage()
		evt := creditEvent("evt-1", "act-1", 10)
		crashing := NewConsumer(st, testBuilders(), WithCrashHook(
			func(id string, committed int) {
				if committed == 2 {
					panic("模拟进程崩溃")
				}
			}))
		tryConsume(crashing, evt)

		restarted := NewConsumer(st, testBuilders())
		undo := ChangeEvent{
			EventID: "evt-undo", ActionExecutionID: "act-undo",
			Kind:    KindActionReverted,
			Payload: map[string]any{"OriginalActionExecutionID": "act-1"},
		}
		if res := restarted.Consume(undo); res.Outcome != OutcomeNoAction {
			t.Fatalf("补偿已越过不可逆点，撤销应为 NoAction，得到 %v", res.Outcome)
		}
		// 补偿必须继续完成。
		res := restarted.Consume(evt)
		if res.Outcome != OutcomeCompensated {
			t.Fatalf("补偿应续作完成，得到 %v", res.Outcome)
		}
		if got := field(t, st, "a", "count"); got != 1 {
			t.Fatalf("a.count 应为 1，得到 %d", got)
		}
	})
}

// TestConcurrentRedelivery 并发重复投递同一事件：恰好一个补偿生效。
func TestConcurrentRedelivery(t *testing.T) {
	st := newTestStorage()
	c := NewConsumer(st, testBuilders())
	evt := creditEvent("evt-1", "act-1", 10)

	const n = 64
	var wg sync.WaitGroup
	outcomes := make([]Outcome, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcomes[i] = c.Consume(evt).Outcome
		}(i)
	}
	wg.Wait()

	compensated := 0
	for _, o := range outcomes {
		if o == OutcomeCompensated {
			compensated++
		}
	}
	if compensated != 1 {
		t.Fatalf("并发下应恰好一次补偿生效，得到 %d 次", compensated)
	}
	if got := field(t, st, "a", "balance"); got != 10 {
		t.Fatalf("a.balance 应为 10，得到 %d", got)
	}
}

// TestConcurrentDistinctEvents 并发消费不同事件：结果等价于某个
// 全局串行顺序（每个补偿恰好生效一次，合计确定）。
func TestConcurrentDistinctEvents(t *testing.T) {
	st := newTestStorage()
	c := NewConsumer(st, testBuilders())

	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			evt := creditEvent(fmt.Sprintf("evt-%d", i), fmt.Sprintf("act-%d", i), 1)
			if res := c.Consume(evt); res.Outcome != OutcomeCompensated {
				t.Errorf("事件 %d 应完成补偿，得到 %v", i, res.Outcome)
			}
		}(i)
	}
	wg.Wait()
	if got := field(t, st, "a", "balance"); got != n {
		t.Fatalf("a.balance 应为 %d，得到 %d", n, got)
	}
	if got := field(t, st, "a", "count"); got != n {
		t.Fatalf("a.count 应为 %d，得到 %d", n, got)
	}
}
