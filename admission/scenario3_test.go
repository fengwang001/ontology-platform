package admission

import (
	"reflect"
	"sync"
	"testing"
)

func oneLevelCfg(total, limit, timeout int64) *Config {
	return &Config{
		TotalSeats: total,
		Levels:     []LevelConfig{limitedLevel("L", 1, limit, timeout)},
		Rules:      []Rule{matchAllRule("r", 1, "L", FlowByUser)},
	}
}

// TestTimeoutBoundary 超时左闭：deadline 当刻失效，先拒绝再触发可能的出队。
func TestTimeoutBoundary(t *testing.T) {
	cfg := oneLevelCfg(1, 10, 10)
	log := newOpLogger(t)
	c := mustController(t, cfg)

	holder := mkReq("h", "", "v", "r", "h", "n", 1)
	r := c.Submit(holder, 0)
	log.logSubmit(holder, 0, r, "holder 立即执行，占满唯一席位")

	wait := mkReq("w", "", "v", "r", "w", "n", 1)
	r = c.Submit(wait, 5)
	log.logSubmit(wait, 5, r, "等待者入队，deadline=15（左闭）")
	if r.Submit.Decision != DecisionQueued {
		t.Fatalf("应排队\n%s", log.dump())
	}

	// deadline 前一刻不得超时。
	before := c.Complete("h", 14)
	log.logComplete("h", 14, before, "t=14 < deadline=15：等待者未超时，释放后立即执行")
	if got := eventIDs(before.Events, "timeout-rejected"); len(got) != 0 {
		t.Fatalf("边界前不应超时\n%s", log.dump())
	}
	if got := eventIDs(before.Events, "executed"); !reflect.DeepEqual(got, []string{"w"}) {
		t.Fatalf("w 应执行: %v\n%s", got, log.dump())
	}

	// 构造第二个等待者验证恰在边界超时。
	doneW := c.Complete("w", 19)
	log.logComplete("w", 19, doneW, "w 执行结束，释放唯一席位")

	hold2 := mkReq("h2", "", "v", "r", "h2", "n", 1)
	r = c.Submit(hold2, 20)
	log.logSubmit(hold2, 20, r, "h2 立即执行占满")
	w2 := mkReq("w2", "", "v", "r", "w2", "n", 1)
	r = c.Submit(w2, 20)
	log.logSubmit(w2, 20, r, "w2 入队，deadline=30")

	res := c.Complete("h2", 30)
	log.logComplete("h2", 30, res, "t==deadline 左闭：完成 h2 释放席位前，w2 先超时被拒；无人执行")
	if got := eventIDs(res.Events, "timeout-rejected"); !reflect.DeepEqual(got, []string{"w2"}) {
		t.Fatalf("w2 应在边界超时: %v\n%s", got, log.dump())
	}
	if got := eventIDs(res.Events, "executed"); len(got) != 0 {
		t.Fatalf("超时后不应执行: %v\n%s", got, log.dump())
	}
	if _, live := c.live["w2"]; live {
		t.Fatalf("超时请求必须从 live 移除\n%s", log.dump())
	}
	t.Log("\n" + log.dump())
}

// TestErrorPriority 同时满足多个错误时只报最高优先级；被拒绝请求不改时钟/轮转。
func TestErrorPriority(t *testing.T) {
	cfg := oneLevelCfg(1, 1, 10)
	log := newOpLogger(t)
	c := mustController(t, cfg)

	// 参数非法优先于一切：即使同时时钟回退。
	bad := mkReq("", "", "v", "r", "u", "n", 100)
	res := c.Submit(bad, 1)
	log.logSubmit(bad, 1, res, "id 空 + 席位越界 + 时钟初始：报 invalid-argument")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassInvalidArgument {
		t.Fatalf("期望 invalid-argument\n%s", log.dump())
	}

	// 先把时钟推进到 10。
	okReq := mkReq("ok", "", "v", "r", "ok", "n", 1)
	res = c.Submit(okReq, 10)
	log.logSubmit(okReq, 10, res, "正常执行，推进时钟到 10")

	// 时钟回退优先于 no-match 与席位/队列等（参数合法时）。
	rewind := mkReq("x", "", "nomatch", "zzz", "u2", "n", 1)
	res = c.Submit(rewind, 9)
	log.logSubmit(rewind, 9, res, "时钟回退且无规则命中：报 clock-rewind（高于 no-match）")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassClockRewind {
		t.Fatalf("期望 clock-rewind\n%s", log.dump())
	}

	// 参数非法优先于无匹配：席位不在 1..10 时，分类前即拒绝。
	nomatch := mkReq("y", "", "nomatch", "zzz", "u3", "n", 11)
	res = c.Submit(nomatch, 11)
	log.logSubmit(nomatch, 11, res, "无匹配 + 席位越界：报 invalid-argument（高于 no-match）")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassInvalidArgument {
		t.Fatalf("期望 invalid-argument\n%s", log.dump())
	}

	// 席位不可满足优先于队列已满：名义 1，请求 2，队列满不满都直接拒绝。
	wide := mkReq("wide", "", "v", "r", "wide-u", "n", 2)
	res = c.Submit(wide, 12)
	log.logSubmit(wide, 12, res, "席位 2 > 名义 1：seat-insatisfiable 优先于 queue-full")
	if res.Submit.Err == nil || res.Submit.Err.Class != ClassInsufficientSeat {
		t.Fatalf("期望 seat-insatisfiable\n%s", log.dump())
	}
	if got := c.Snapshot()["L"]; got != 1 {
		t.Fatalf("拒绝不得改变占用: %d\n%s", got, log.dump())
	}
	t.Log("\n" + log.dump())
}

// TestConcurrent 并发调用结果必须等价某个串行顺序，且占用永不超过名义席位。
func TestConcurrent(t *testing.T) {
	cfg := oneLevelCfg(8, 200, 100000)
	c := mustController(t, cfg)

	const workers = 16
	const perWorker = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	var violations []string
	var executed int64
	var queued int64
	var rejected int64

	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := idFor(g, i)
				req := mkReq(id, "", "v", "r", id, "ns", 1)
				res := c.Submit(req, Time(1+g*perWorker+i))
				mu.Lock()
				switch res.Submit.Decision {
				case DecisionExecuted:
					executed++
				case DecisionQueued:
					queued++
				default:
					rejected++
				}
				for name, used := range c.Snapshot() {
					if used > 8 {
						violations = append(violations, name+":used>8")
					}
				}
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	if len(violations) != 0 {
		t.Fatalf("占用越界: %v", violations)
	}
	t.Logf("并发提交结束 executed=%d queued=%d rejected=%d", executed, queued, rejected)
}

func idFor(g, i int) string {
	return "g" + itoa(g) + "-" + itoa(i)
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	var b []byte
	for x > 0 {
		b = append([]byte{byte('0' + x%10)}, b...)
		x /= 10
	}
	return string(b)
}
