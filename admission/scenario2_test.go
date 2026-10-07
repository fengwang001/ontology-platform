package admission

import (
	"reflect"
	"testing"
)

// TestQueueFullAndWideRequest 宽请求直接拒绝；队列满；拒绝不改状态。
func TestQueueFullAndWideRequest(t *testing.T) {
	cfg := &Config{
		TotalSeats: 2,
		Levels:     []LevelConfig{limitedLevel("L", 1, 2, 100)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	wide := mkReq("wide", "", "v", "r", "u-wide", "ns1", 3)
	res := c.Submit(wide, 1)
	log.logSubmit(wide, 1, res, "席位 3 > 名义 2，永远不可满足，直接拒绝")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassInsufficientSeat {
		t.Fatalf("宽请求应不可满足\n%s", log.dump())
	}

	occupy := mkReq("occ", "", "v", "r", "u-occ", "ns1", 2)
	res = c.Submit(occupy, 1)
	log.logSubmit(occupy, 1, res, "2 席位立即执行占满")

	q1 := mkReq("q1", "", "v", "r", "u1", "ns1", 1)
	res = c.Submit(q1, 2)
	log.logSubmit(q1, 2, res, "席位不足 → 排队")
	q2 := mkReq("q2", "", "v", "r", "u2", "ns1", 1)
	res = c.Submit(q2, 3)
	log.logSubmit(q2, 3, res, "队列第二格 → 排队")

	q3 := mkReq("q3", "", "v", "r", "u3", "ns1", 1)
	res = c.Submit(q3, 4)
	log.logSubmit(q3, 4, res, "队列已满（上限2）→ queue-full")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassQueueFull {
		t.Fatalf("应队列已满\n%s", log.dump())
	}
	if snap := c.Snapshot(); snap["L"] != 2 {
		t.Fatalf("被拒绝请求不得改变占用: %v\n%s", snap, log.dump())
	}

	// 释放 2 席位：同一完成操作内 pump 出两个排队者。
	res = c.Complete("occ", 5)
	log.logComplete("occ", 5, res, "释放后同操作内出队，轮转锚点依次服务 u1,u2")
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"q1", "q2"}) {
		t.Fatalf("出队顺序错误: %v\n%s", got, log.dump())
	}
	if snap := c.Snapshot(); snap["L"] != 2 {
		t.Fatalf("释放后应被两个排队者重新占满: %v\n%s", snap, log.dump())
	}
	t.Log("\n" + log.dump())
}

// TestHeadOfLineBlockingPrecise 精确构造：4 席在途，E(宽5)、F(窄1) 排队。
func TestHeadOfLineBlockingPrecise(t *testing.T) {
	cfg := &Config{
		TotalSeats: 5,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	big := mkReq("big", "", "v", "r", "ubig", "ns1", 4)
	r := c.Submit(big, 1)
	log.logSubmit(big, 1, r, "big 占 4 席立即执行，剩余 1")

	blocker := mkReq("blocker", "", "v", "r", "userE", "ns1", 5)
	r = c.Submit(blocker, 2)
	log.logSubmit(blocker, 2, r, "E 宽请求 5 席，剩余 1 放不下，排队")
	victim := mkReq("victim", "", "v", "r", "userF", "ns1", 1)
	r = c.Submit(victim, 3)
	log.logSubmit(victim, 3, r, "F 窄请求 1 席，恰好放得下但排在 E 之后")

	// 完成一个 1 席的“在途请求”才能只腾 1 席：当前在途只有 big(4)。
	// 故再引入：让 victim 不可能先执行（已满足队首阻塞条件），
	// 直接完成 big 会释放 4 席 → blocker(5) 仍放不下（used=0 时实际放得下！）。
	// big 完成后 used=0，blocker 5<=5 会执行——这与“阻塞”不冲突：阻塞只发生在剩余不足时。
	res := c.Complete("big", 4)
	log.logComplete("big", 4, res, "释放后 E(blocker,5) 放得下并占满；F(victim,1) 因满额继续等待")
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"blocker"}) {
		t.Fatalf("blocker 占满后 victim 必须继续等待: %v\n%s", got, log.dump())
	}

	// 真正的阻塞：在途 1 席，E(5)、F(1) 排队。需要一个完成后只释放 1 的场景，
	// 通过“先占满再替换”：占 1 席 holder + 已在途 4 席 big2 同时存在（used=5）。
	// big2 不存在执行可能（满额），所以用两个在途请求构造：4+1。
	// 重置控制器更清晰：
	c2 := mustController(t, &Config{
		TotalSeats: 5,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	})
	log2 := newOpLogger(t)
	occ4 := mkReq("occ4", "", "v", "r", "o4", "ns1", 4)
	r = c2.Submit(occ4, 10)
	log2.logSubmit(occ4, 10, r, "occ4 立即执行占 4")
	occ1 := mkReq("occ1", "", "v", "r", "o1", "ns1", 1)
	r = c2.Submit(occ1, 11)
	log2.logSubmit(occ1, 11, r, "occ1 立即执行占 1（满额 used=5）")
	wide := mkReq("wide", "", "v", "r", "userE", "ns1", 5)
	r = c2.Submit(wide, 12)
	log2.logSubmit(wide, 12, r, "E 宽 5 排队")
	narrow := mkReq("narrow", "", "v", "r", "userF", "ns1", 1)
	r = c2.Submit(narrow, 13)
	log2.logSubmit(narrow, 13, r, "F 窄 1 排队")

	res = c2.Complete("occ1", 14)
	log2.logComplete("occ1", 14, res, "仅释放 1 席（剩余1）：E 需 5 放不下，整级停止，F 不得越过")
	if got := eventIDs(res.Events, "executed"); len(got) != 0 {
		t.Fatalf("队首阻塞时不应出队: %v\n%s", got, log2.dump())
	}
	if snap := c2.Snapshot(); snap["L"] != 4 {
		t.Fatalf("占用应保持 4: %v\n%s", snap, log2.dump())
	}

	res = c2.Complete("occ4", 15)
	log2.logComplete("occ4", 15, res, "再释放 4（剩余5）：E(5) 先出并占满，F(1) 继续等")
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"wide"}) {
		t.Fatalf("E 出队后应占满，F 继续等待: %v\n%s", got, log2.dump())
	}

	res = c2.Complete("wide", 16)
	log2.logComplete("wide", 16, res, "宽请求完成后释放 5 席，F(narrow) 才出队")
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"narrow"}) {
		t.Fatalf("应只服务 narrow: %v\n%s", got, log2.dump())
	}
	t.Log("\n" + log.dump() + "\n" + log2.dump())
}

// TestFlowRotation 流内 FIFO、流间激活顺序轮转、不得插队。
func TestFlowRotation(t *testing.T) {
	cfg := &Config{
		TotalSeats: 1,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	first := mkReq("a0", "", "v", "r", "userA", "ns1", 1)
	r := c.Submit(first, 1)
	log.logSubmit(first, 1, r, "A 流首个请求立即执行，占满唯一席位")

	// 激活顺序：A、B、C；A 内部 a1,a2 保证流内 FIFO。
	queue := []struct{ id, user string }{
		{"a1", "userA"}, {"b1", "userB"}, {"a2", "userA"},
		{"c1", "userC"}, {"b2", "userB"},
	}
	for i, q := range queue {
		req := mkReq(q.id, "", "v", "r", q.user, "ns1", 1)
		rr := c.Submit(req, Time(2+i))
		log.logSubmit(req, Time(2+i), rr, "有等待者，必须排队不得插队")
		if rr.Submit.Decision != DecisionQueued {
			t.Fatalf("%s 应排队\n%s", q.id, log.dump())
		}
	}

	// 完成 a0 后，流按激活顺序 A→B→C 循环；同流 FIFO：
	// a1(A) → b1(B) → c1(C) → a2(A，绕回) → b2(B)。
	res := c.Complete("a0", 10)
	log.logComplete("a0", 10, res, "pump 顺序应为 a1,b1,c1,a2,b2")
	// 单席位：一次 pump 只会出队 a1（之后 used=1 满额），需要逐个完成。
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("首次 pump 只应出 a1: %v\n%s", got, log.dump())
	}

	want := []string{"b1", "c1", "a2", "b2"}
	prev := "a1"
	for i, id := range want {
		res = c.Complete(prev, Time(11+i))
		log.logComplete(prev, Time(11+i), res, "完成后 pump 下一个轮转流")
		got := eventIDs(res.Events, "executed")
		if !reflect.DeepEqual(got, []string{id}) {
			t.Fatalf("第 %d 轮应出 %s, got %v\n%s", i, id, got, log.dump())
		}
		prev = id
	}
	t.Log("\n" + log.dump())
}
