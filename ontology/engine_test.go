package ontology

import "testing"

// 基数恰好用尽：先占满名额，新请求基于最新读取被业务拒绝，版本零变化。
func TestCardinalityExactlyFull(t *testing.T) {
	env := newConstrainedEnv(t, 3, nil, 1)
	env.seedLinks(t, "y0")

	req := addReq("y1")
	req.Baseline = env.version(t)
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Reject == nil || res.Reject.Code != CodeCardinality {
		t.Fatalf("want cardinality rejection, got %+v", res)
	}
	if len(res.Attempts) != 1 || res.Attempts[0].Outcome != OutcomeCardinality {
		t.Fatalf("want single cardinality attempt, got %+v", res.Attempts)
	}
	v := res.Attempts[0].Verdicts[0]
	if v.Current != 1 || v.Delta != 1 || v.Max != 1 || v.Satisfied {
		t.Fatalf("bad verdict %+v", v)
	}
	if env.version(t) != 1 {
		t.Fatalf("version must not change, got %d", env.version(t))
	}
}

// 恰好空出一个名额：当前已满，先有移除插队空出名额，重试必须成功且只成功一次。
func TestCardinalityExactlyOneFreed(t *testing.T) {
	env := newConstrainedEnv(t, 3, nil, 1)
	env.seedLinks(t, "y0") // 名额被 y0 占用，版本 1

	hook := &toggleHook{
		store: env.store,
		schedule: map[int][]Op{
			1: {{TypeID: testLinkType, Side: SideA, Other: "y0", Add: false}},
		},
	}
	env.eng = NewEngine(env.store, 5, hook.hook)

	req := addReq("y1")
	req.Baseline = 0 // 故意落后基线，制造首次版本冲突
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed {
		t.Fatalf("want commit after slot freed, got %+v", res.Reject)
	}
	if len(res.Attempts) != 2 ||
		res.Attempts[0].Outcome != OutcomeConflict ||
		res.Attempts[1].Outcome != OutcomeCommitted {
		t.Fatalf("want conflict then commit, got %+v", res.Attempts)
	}
	links, _ := env.store.LinkSnapshot("x")
	if len(links) != 1 || links[0].B != "y1" {
		t.Fatalf("want exactly y1 linked, got %+v", links)
	}
}

// 重试过程中基数状态反复变化：不满足→满足→又被抢占，最终预算耗尽。
// 终态必须明确为重试耗尽，而不是基数业务拒绝。
func TestCardinalityOscillatesThenExhausted(t *testing.T) {
	env := newConstrainedEnv(t, 4, nil, 1)
	env.seedLinks(t, "y0") // 满

	hook := &toggleHook{store: env.store, schedule: map[int][]Op{
		// 尝试1：基线 0 落后即冲突；插队者原子地把占用者 y0 换成 y1（仍满）。
		1: {
			{TypeID: testLinkType, Side: SideA, Other: "y0", Add: false},
			{TypeID: testLinkType, Side: SideA, Other: "y1", Add: true},
		},
		// 尝试2：原子替换为 y2，模拟基数状态的反复变化。
		2: {
			{TypeID: testLinkType, Side: SideA, Other: "y1", Add: false},
			{TypeID: testLinkType, Side: SideA, Other: "y2", Add: true},
		},
		// 尝试3：再次反复
		3: {
			{TypeID: testLinkType, Side: SideA, Other: "y2", Add: false},
			{TypeID: testLinkType, Side: SideA, Other: "y3", Add: true},
		},
		// 尝试4（最后一次）：仍然被抢先替换 -> 版本冲突，预算耗尽
		4: {
			{TypeID: testLinkType, Side: SideA, Other: "y3", Add: false},
			{TypeID: testLinkType, Side: SideA, Other: "y4", Add: true},
		},
	}}
	env.eng = NewEngine(env.store, 4, hook.hook)

	req := addReq("y9")
	req.Baseline = 0
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Reject == nil || res.Reject.Code != CodeRetriesExhausted {
		t.Fatalf("want retry exhaustion, got committed=%v reject=%+v", res.Committed, res.Reject)
	}
	if len(res.Attempts) != 4 {
		t.Fatalf("want exactly 4 attempts, got %d", len(res.Attempts))
	}
	for i, a := range res.Attempts {
		if a.Outcome != OutcomeConflict {
			t.Fatalf("attempt %d must be version conflict (single reason), got %v", i+1, a.Outcome)
		}
	}
	// 每次尝试的快照必须反映“当次重新读取”到的不同占用者。
	want := []string{"y1", "y2", "y3", "y4"}
	for i, a := range res.Attempts {
		if len(a.Snapshot) != 1 || a.Snapshot[0].B != want[i] {
			t.Fatalf("attempt %d snapshot want %s, got %+v", i+1, want[i], a.Snapshot)
		}
	}
}

// 重试预算恰好耗尽：最后一次尝试仍冲突时归类为重试耗尽。
func TestRetryBudgetExactlyExhausted(t *testing.T) {
	env := newConstrainedEnv(t, 3, nil, 10)
	hook := &raiderHook{store: env.store, raids: map[int]string{
		1: "y1", 2: "y2", 3: "y3",
	}}
	env.eng = NewEngine(env.store, 3, hook.hook)

	req := addReq("y0")
	req.Baseline = 0
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Reject.Code != CodeRetriesExhausted {
		t.Fatalf("want exhausted, got %+v", res)
	}
	// 失败的中间尝试不得改变目标实例的最终关联：三次插队链接仍在，y0 不在。
	links, _ := env.store.LinkSnapshot("x")
	if len(links) != 3 {
		t.Fatalf("want 3 links (no y0), got %+v", links)
	}
	for _, lk := range links {
		if lk.B == "y0" {
			t.Fatal("failed attempt leaked a link")
		}
	}
}

// 预算内最后一次尝试成功：不允许提前判为耗尽。
func TestCommitOnLastAttempt(t *testing.T) {
	env := newConstrainedEnv(t, 3, nil, 10)
	hook := &raiderHook{store: env.store, raids: map[int]string{
		1: "y1", 2: "y2",
	}}
	env.eng = NewEngine(env.store, 3, hook.hook)

	req := addReq("y0")
	req.Baseline = 0
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || len(res.Attempts) != 3 {
		t.Fatalf("want commit on 3rd attempt, got %+v", res)
	}
}

// 落后基线下当前基数不满足：版本冲突优先命中，但当次最新读取的基数仍被记录。
func TestVersionConflictPrecedesCardinality(t *testing.T) {
	env := newConstrainedEnv(t, 3, nil, 1)
	env.seedLinks(t, "y0")
	req := addReq("y1")
	req.Baseline = 0 // 落后；当前版本为 1，且当前已满
	res, err := env.eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	// 尝试1：版本冲突优先（即使此刻基数也已满，本次只命中冲突一种原因）。
	// 尝试2：基线追平，在最新读取上做基数校验 -> 业务拒绝终态（优先于“耗尽”）。
	if res.Committed || res.Reject.Code != CodeCardinality {
		t.Fatalf("want terminal cardinality once baseline catches up, got %+v", res.Reject)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(res.Attempts))
	}
	first := res.Attempts[0]
	if first.Outcome != OutcomeConflict {
		t.Fatalf("want conflict outcome, got %v", first.Outcome)
	}
	// 当次最新读取到的基数依据必须完整记录（current=1, delta=1, unsatisfied）。
	v := first.Verdicts[0]
	if v.Current != 1 || v.Delta != 1 || v.Satisfied {
		t.Fatalf("want recorded unsatisfied verdict, got %+v", v)
	}
	if res.Attempts[1].Outcome != OutcomeCardinality {
		t.Fatalf("want 2nd attempt cardinality, got %v", res.Attempts[1].Outcome)
	}
}
