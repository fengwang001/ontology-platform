package admission

import "testing"

func mustController(t *testing.T, cfg *Config) *Controller {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func mkReq(id, group, verb, resource, user, ns string, seats int64) *Request {
	return &Request{
		ID: id, UserGroup: group, Verb: verb, Resource: resource,
		User: user, Namespace: ns, Seats: seats,
	}
}

func limitedLevel(name string, share, limit, timeout int64) LevelConfig {
	return LevelConfig{Name: name, Kind: LevelLimited, Share: share, QueueLimit: limit, Timeout: timeout}
}

func matchAllRule(name string, priority int64, level string, d FlowDistinguish) Rule {
	return Rule{Name: name, Priority: priority, TargetLevel: level, Distinguish: d}
}

// TestRuleOrder 规则优先级数值小者优先；数值相同按名称升序；无匹配报错。
func TestRuleOrder(t *testing.T) {
	cfg := &Config{
		TotalSeats: 10,
		Levels: []LevelConfig{
			{Name: "A", Kind: LevelLimited, Share: 1, QueueLimit: 10, Timeout: 100},
			{Name: "B", Kind: LevelLimited, Share: 1, QueueLimit: 10, Timeout: 100},
		},
		Rules: []Rule{
			{Name: "r-low", Priority: 10, Match: MatchConditions{Verbs: []string{"read"}}, TargetLevel: "A", Distinguish: FlowByUser},
			{Name: "r-b", Priority: 5, Match: MatchConditions{Resources: []string{"x"}}, TargetLevel: "B", Distinguish: FlowByUser},
			{Name: "r-a", Priority: 5, Match: MatchConditions{Resources: []string{"x"}}, TargetLevel: "A", Distinguish: FlowByUser},
		},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	r1 := mkReq("q1", "", "", "x", "u1", "ns1", 1)
	res1 := c.Submit(r1, 1)
	log.logSubmit(r1, 1, res1, "priority=5 两条规则同值，名称升序先命中 r-a → level A")
	if res1.Submit.Level != "A" {
		t.Fatalf("同值按名称升序失败: got %s\n%s", res1.Submit.Level, log.dump())
	}

	r2 := mkReq("q2", "", "read", "y", "u2", "ns1", 1)
	res2 := c.Submit(r2, 2)
	log.logSubmit(r2, 2, res2, "只有 priority=10 的 r-low 命中 → level A")
	if res2.Submit.Level != "A" {
		t.Fatalf("低优先级规则匹配失败\n%s", log.dump())
	}

	r3 := mkReq("q3", "", "write", "z", "u3", "ns1", 1)
	res3 := c.Submit(r3, 3)
	log.logSubmit(r3, 3, res3, "无规则命中 → no-match")
	if res3.Submit.Err == nil || res3.Submit.Err.Class != ClassNoMatch {
		t.Fatalf("期望 no-match\n%s", log.dump())
	}
	t.Log("\n" + log.dump())
}

// TestSeatAllocationRounding 向下取整 + 余量按份额降序（同份额按名称升序）。
func TestSeatAllocationRounding(t *testing.T) {
	levels := []LevelConfig{
		{Name: "hi", Kind: LevelLimited, Share: 50},
		{Name: "lo1", Kind: LevelLimited, Share: 1},
		{Name: "lo2", Kind: LevelLimited, Share: 1},
	}
	shares := map[string]int64{"hi": 50, "lo1": 1, "lo2": 1}

	got := allocateSeats(10, levels, shares)
	want := map[string]int64{"hi": 10, "lo1": 0, "lo2": 0}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("total=10 分配错误: got %v want %v", got, want)
		}
	}

	// total=103：floor 为 99/1/1（和101），余量 2 依次补 hi、lo1（同份额名称升序）。
	got = allocateSeats(103, levels, shares)
	want = map[string]int64{"hi": 100, "lo1": 2, "lo2": 1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("total=103 分配错误: got %v want %v", got, want)
		}
	}
	var sum int64
	for _, v := range got {
		sum += v
	}
	if sum != 103 {
		t.Fatalf("席位不守恒: %d", sum)
	}
}

// TestExemptAndZeroNominal 豁免立即执行不占席位；零名义席位全部不可满足。
func TestExemptAndZeroNominal(t *testing.T) {
	cfg := &Config{
		TotalSeats: 3,
		Levels: []LevelConfig{
			{Name: "free", Kind: LevelExempt},
			limitedLevel("big", 100, 5, 100),
			limitedLevel("tiny", 1, 5, 100),
		},
		Rules: []Rule{
			{Name: "ex", Priority: 1, Match: MatchConditions{Verbs: []string{"read"}}, TargetLevel: "free", Distinguish: FlowByUser},
			matchAllRule("big-r", 2, "big", FlowByUser),
		},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	read := mkReq("e1", "", "read", "r", "u1", "ns1", 10)
	res := c.Submit(read, 1)
	log.logSubmit(read, 1, res, "豁免级别：10 席位也立即执行，不占席位")
	if res.Submit.Decision != DecisionExecuted {
		t.Fatalf("豁免请求应立即执行\n%s", log.dump())
	}

	for _, id := range []string{"b1", "b2", "b3"} {
		req := mkReq(id, "", "write", "r", id, "ns1", 1)
		r := c.Submit(req, 1)
		log.logSubmit(req, 1, r, "big 名义席位=3，前三个立即执行")
		if r.Submit.Decision != DecisionExecuted {
			t.Fatalf("%s 应执行\n%s", id, log.dump())
		}
	}
	if snap := c.Snapshot(); snap["big"] != 3 {
		t.Fatalf("big 占用应为3: %v\n%s", snap, log.dump())
	}

	c2 := mustController(t, &Config{
		TotalSeats: 3,
		Levels: []LevelConfig{
			limitedLevel("big", 100, 5, 100),
			limitedLevel("tiny", 1, 5, 100),
		},
		Rules: []Rule{matchAllRule("tiny-r", 1, "tiny", FlowByUser)},
	})
	req := mkReq("t0", "", "write", "r", "u", "ns1", 1)
	r := c2.Submit(req, 1)
	log.logSubmit(req, 1, r, "tiny 名义席位=0 → seat-insatisfiable（优先于排队）")
	if r.Submit.Err == nil || r.Submit.Err.Class != ClassInsufficientSeat {
		t.Fatalf("零名义席位应拒绝为不可满足\n%s", log.dump())
	}
	t.Log("\n" + log.dump())
}
