package admission

import (
	"reflect"
	"testing"
)

// TestConfigUpdate 在途不打断、排队继续；新名义席位不足时排队者在更新时即拒绝。
func TestConfigUpdate(t *testing.T) {
	cfg := &Config{
		TotalSeats: 10,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	log := newOpLogger(t)
	c := mustController(t, cfg)

	runner := mkReq("runner", "", "v", "r", "runner", "n", 6)
	r := c.Submit(runner, 1)
	log.logSubmit(runner, 1, r, "runner 占 6 席立即执行")

	w5 := mkReq("w5", "", "v", "r", "u5", "n", 5)
	r = c.Submit(w5, 2)
	log.logSubmit(w5, 2, r, "w5 需 5，剩余 4，排队")
	w2 := mkReq("w2", "", "v", "r", "u2", "n", 2)
	r = c.Submit(w2, 3)
	log.logSubmit(w2, 3, r, "w2 需 2，排队")

	// 新总席位 6：runner 在途 6 合法；w5 需要 5 <= 6 保留；w2 需要 2 保留。
	cfg6 := &Config{
		TotalSeats: 6,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	up, err := c.UpdateConfig(cfg6, 4)
	log.logUpdate(4, up, err, "名义席位从10降为6：runner 在途不打断，排队者按旧级别排队、受新席位约束")
	if err != nil {
		t.Fatalf("更新应生效: %v\n%s", err, log.dump())
	}
	if got := c.Snapshot()["L"]; got != 6 {
		t.Fatalf("在途占用应保持6: %d\n%s", got, log.dump())
	}

	// runner 完成后 used=0：w5(5) 先执行并占 5；w2(2) 剩 1 放不下继续等。
	res := c.Complete("runner", 5)
	log.logComplete("runner", 5, res, "释放后 w5(5) 执行；w2(2) 因只剩1继续等待")
	if got := eventIDs(res.Events, "executed"); !reflect.DeepEqual(got, []string{"w5"}) {
		t.Fatalf("应只服务 w5: %v\n%s", got, log.dump())
	}

	// 非法更新（把 L 变成豁免，但有在途/排队）整体不生效。
	bad := &Config{
		TotalSeats: 10,
		Levels:     []LevelConfig{{Name: "L", Kind: LevelExempt}},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	_, err = c.UpdateConfig(bad, 6)
	log.logUpdate(6, nil, err, "L 从受限变豁免且仍有 live 请求：非法，整体不生效")
	if err == nil {
		t.Fatalf("应拒绝非法更新\n%s", log.dump())
	}
	if got := c.Snapshot()["L"]; got != 5 {
		t.Fatalf("非法更新不得改变名义/占用: %d\n%s", got, log.dump())
	}

	// 完成 w5：used=0，w2(2) 按队列执行，占用2。
	done := c.Complete("w5", 7)
	log.logComplete("w5", 7, done, "w5 完成，w2(2) 出队执行")
	if got := eventIDs(done.Events, "executed"); !reflect.DeepEqual(got, []string{"w2"}) {
		t.Fatalf("应只服务 w2: %v\n%s", got, log.dump())
	}

	// 此时 used=2，名义=6；提交 bq(5)：不超名义但剩余4不足，排队。
	bigQueued := mkReq("bq", "", "v", "r", "ubq", "n", 5)
	r = c.Submit(bigQueued, 8)
	log.logSubmit(bigQueued, 8, r, "bq 需5，当前剩余4，排队（名义仍为6）")
	if r.Submit.Decision != DecisionQueued {
		t.Fatalf("bq 应排队\n%s", log.dump())
	}

	cfg4 := &Config{
		TotalSeats: 4,
		Levels:     []LevelConfig{limitedLevel("L", 1, 100, 1000)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
	up4, err := c.UpdateConfig(cfg4, 9)
	log.logUpdate(9, up4, err, "名义降为4：w2 在途2合法；bq(5)>4 在更新时即拒绝")
	if err != nil {
		t.Fatalf("更新应生效: %v\n%s", err, log.dump())
	}
	if got := eventIDs(up4.Events, "update-rejected"); !reflect.DeepEqual(got, []string{"bq"}) {
		t.Fatalf("bq 应更新时拒绝: %v\n%s", got, log.dump())
	}
	if _, live := c.live["bq"]; live {
		t.Fatalf("bq 必须从 live 移除\n%s", log.dump())
	}

	done2 := c.Complete("w2", 10)
	log.logComplete("w2", 10, done2, "w2 完成后队列已空")
	if got := eventIDs(done2.Events, "executed"); len(got) != 0 {
		t.Fatalf("bq 已拒绝，不应再执行: %v\n%s", got, log.dump())
	}
	t.Log("\n" + log.dump())
}
