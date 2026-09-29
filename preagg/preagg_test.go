package preagg

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
)

// dump 打印输入、结果与判定依据，满足测试日志可追溯要求。
func dump(t *testing.T, stage, input string, got any, reason string) {
	t.Helper()
	t.Logf("[%s] input=%s result=%v verdict=%s", stage, input, got, reason)
}

func TestNewInvalidThreshold(t *testing.T) {
	for _, th := range []int{0, -1, -100} {
		p, err := New(th)
		if !errors.Is(err, ErrInvalidThreshold) {
			t.Fatalf("New(%d) err=%v, want ErrInvalidThreshold", th, err)
		}
		if p != nil {
			t.Fatalf("New(%d) returned non-nil aggregator on error", th)
		}
		dump(t, "New", fmt.Sprintf("threshold=%d", th), err, "阈值非正 -> ErrInvalidThreshold，且无实例产生")
	}
}

// TestThresholdTrigger 校验攒满阈值（含阈值本身）立即推送并成为热键。
func TestThresholdTrigger(t *testing.T) {
	p, err := New(3)
	if err != nil {
		t.Fatal(err)
	}

	// 前两条：分两批喂入（同批重复会触发批末热键晋升），攒批且视图不含缓冲。
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 2}}); err != nil {
		t.Fatal(err)
	}
	if got := p.View(); len(got) != 0 {
		t.Fatalf("未达阈值时视图应为空, got=%v", got)
	}
	if got := p.Pending()["a"]; got != 3 {
		t.Fatalf("待推送增量应为 3, got=%d", got)
	}
	dump(t, "Threshold-未触发", "batch=[a+1]; batch=[a+2]",
		fmt.Sprintf("view=%v pending=%v", p.View(), p.Pending()), "已攒 2<3，留在本地缓冲")

	// 第三条达到阈值：立即推送一条汇总变更并清空缓冲、标记热键。
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 4}}); err != nil {
		t.Fatal(err)
	}
	if got := p.View()["a"]; got != 7 {
		t.Fatalf("达到阈值后视图 a 应为 7, got=%d", got)
	}
	if _, ok := p.Pending()["a"]; ok {
		t.Fatalf("达到阈值后缓冲应被清空, got=%v", p.Pending())
	}
	if !p.HotKeys()["a"] {
		t.Fatalf("达到阈值后 a 应为热键, got=%v", p.HotKeys())
	}
	dump(t, "Threshold-触发", "batch=[a+4]",
		fmt.Sprintf("view=%v pending=%v hot=%v log=%v", p.View(), p.Pending(), p.HotKeys(), p.PushLog()),
		"已攒 3>=3：汇总增量 +7 推送，缓冲清空，a 标记热键")

	// 热键之后每条事件立即单独推送，不再攒批；即使同批两条也各产生一条推送。
	if err := p.FeedBatch([]Event{{Key: "a", Delta: -5}}); err != nil {
		t.Fatal(err)
	}
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 10}}); err != nil {
		t.Fatal(err)
	}
	if got := p.View()["a"]; got != 12 {
		t.Fatalf("热键逐条推送后 a 应为 12, got=%d", got)
	}
	if _, ok := p.Pending()["a"]; ok {
		t.Fatalf("热键不应产生缓冲, got=%v", p.Pending())
	}
	wantLog := []Event{{Key: "a", Delta: 7}, {Key: "a", Delta: -5}, {Key: "a", Delta: 10}}
	if got := p.PushLog(); !slices.Equal(got, wantLog) {
		t.Fatalf("推送记录=%v, want=%v", got, wantLog)
	}
	dump(t, "Threshold-热键逐条", "batch=[a-5]; batch=[a+10]",
		fmt.Sprintf("view=%v log=%v", p.View(), p.PushLog()),
		"热键每到达一条立即单独推送，记录为 -5 与 +10 两条")

	if err := p.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestHotKeyPromotionAndClearing 校验同批重复键在批末晋升热键并冲刷缓冲，
// 以及 FlushKey 解除热键后重新攒批。
func TestHotKeyPromotionAndClearing(t *testing.T) {
	p, _ := New(10)

	// 同一批次 b 出现两次：批末 b 成为热键，缓冲（+3）先冲刷为一条推送。
	if err := p.FeedBatch([]Event{{Key: "b", Delta: 1}, {Key: "c", Delta: 1}, {Key: "b", Delta: 2}}); err != nil {
		t.Fatal(err)
	}
	if !p.HotKeys()["b"] || p.HotKeys()["c"] {
		t.Fatalf("仅 b 应为热键, got=%v", p.HotKeys())
	}
	if got := p.View()["b"]; got != 3 {
		t.Fatalf("批末晋升应先冲刷 b 缓冲 +3, got view=%d", got)
	}
	if _, ok := p.Pending()["b"]; ok {
		t.Fatalf("热键 b 不应残留缓冲, got=%v", p.Pending())
	}
	if got := p.Pending()["c"]; got != 1 {
		t.Fatalf("普通键 c 应保留待推送 +1, got=%d", got)
	}
	dump(t, "HotKey-批末晋升", "batch=[b+1,c+1,b+2]",
		fmt.Sprintf("view=%v pending=%v hot=%v log=%v", p.View(), p.Pending(), p.HotKeys(), p.PushLog()),
		"b 同批出现 2 次：批末先把缓冲 +3 推送，再标记热键；c 出现 1 次仍攒批")

	// FlushKey 冲刷 c 的待推送增量；对热键 b 调用则解除热键（无缓冲故不推送）。
	flushed, err := p.FlushKey("c")
	if err != nil || !flushed {
		t.Fatalf("FlushKey(c) = (%v,%v), want (true,nil)", flushed, err)
	}
	if got := p.View()["c"]; got != 1 {
		t.Fatalf("FlushKey 后 c 视图应为 1, got=%d", got)
	}
	flushed, err = p.FlushKey("b")
	if err != nil || flushed {
		t.Fatalf("FlushKey(b) = (%v,%v), want (false,nil)（热键无缓冲）", flushed, err)
	}
	if p.HotKeys()["b"] {
		t.Fatalf("FlushKey 后 b 应移出热键集合, got=%v", p.HotKeys())
	}
	dump(t, "HotKey-单键冲刷/解除", "FlushKey(c); FlushKey(b)",
		fmt.Sprintf("view=%v hot=%v", p.View(), p.HotKeys()),
		"FlushKey 推送待推送增量并无条件移出热键集合；b 无缓冲故不产生推送")

	// b 解除热键后重新按普通键攒批。
	if err := p.FeedBatch([]Event{{Key: "b", Delta: 9}}); err != nil {
		t.Fatal(err)
	}
	if p.HotKeys()["b"] || p.View()["b"] != 3 {
		t.Fatalf("解除后 b 应重新攒批: view=%d hot=%v", p.View()["b"], p.HotKeys())
	}
	if got := p.Pending()["b"]; got != 9 {
		t.Fatalf("解除后 b 的新事件应进入缓冲, got=%d", got)
	}
	dump(t, "HotKey-解除后恢复攒批", "batch=[b+9]",
		fmt.Sprintf("view=%v pending=%v hot=%v", p.View(), p.Pending(), p.HotKeys()),
		"b 已非热键，+9 进入缓冲等待攒满")
}

// TestFlushAll 校验全量冲刷的字典序、视图与热键清空语义。
func TestFlushAll(t *testing.T) {
	p, _ := New(5)
	// 让 d 成为热键（同批两次，批末冲刷 +4）；e、a 保留缓冲。
	if err := p.FeedBatch([]Event{
		{Key: "d", Delta: 1}, {Key: "e", Delta: 2},
		{Key: "d", Delta: 3}, {Key: "a", Delta: 7},
	}); err != nil {
		t.Fatal(err)
	}

	p.FlushAll()
	if len(p.Pending()) != 0 {
		t.Fatalf("FlushAll 后缓冲应清空, got=%v", p.Pending())
	}
	if len(p.HotKeys()) != 0 {
		t.Fatalf("FlushAll 后热键集合应清空, got=%v", p.HotKeys())
	}
	if got := p.View(); got["a"] != 7 || got["d"] != 4 || got["e"] != 2 {
		t.Fatalf("FlushAll 后视图不符: %v", got)
	}
	// 推送顺序：批末晋升先推送 d(+4)，FlushAll 再按字典序 a,e。
	wantLog := []Event{{Key: "d", Delta: 4}, {Key: "a", Delta: 7}, {Key: "e", Delta: 2}}
	if got := p.PushLog(); !slices.Equal(got, wantLog) {
		t.Fatalf("推送记录=%v, want=%v（FlushAll 部分须按字典序 a,e）", got, wantLog)
	}
	dump(t, "FlushAll", "buffer={a:+7,e:+2}, hot={d}",
		fmt.Sprintf("view=%v hot=%v log=%v", p.View(), p.HotKeys(), p.PushLog()),
		"所有待推送键按字典序 a<e 逐个推送，全部键移出热键集合")

	// 无缓冲无热键时 FlushAll 幂等。
	p.FlushAll()
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestRejectedBatchesLeaveNoTrace 覆盖四类非法输入：整批拒绝、失败不留状态痕迹、可继续使用。
func TestRejectedBatchesLeaveNoTrace(t *testing.T) {
	type badCase struct {
		name   string
		events []Event
		want   error
	}
	cases := []badCase{
		{"空批次-nil", nil, ErrEmptyBatch},
		{"空批次-空切片", []Event{}, ErrEmptyBatch},
		{"空键", []Event{{Key: "a", Delta: 1}, {Key: "", Delta: 2}}, ErrEmptyKey},
		{"零增量", []Event{{Key: "a", Delta: 1}, {Key: "b", Delta: 0}}, ErrZeroDelta},
		{"混合非法-空键优先", []Event{{Key: "", Delta: 0}, {Key: "a", Delta: 1}}, ErrEmptyKey},
	}

	p, _ := New(3)
	// 先建立合法基线状态。
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 5}}); err != nil {
		t.Fatal(err)
	}
	baseView, basePending, baseHot, baseLog := p.View(), p.Pending(), p.HotKeys(), p.PushLog()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := p.FeedBatch(c.events)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			if !maps.Equal(p.View(), baseView) || !maps.Equal(p.Pending(), basePending) ||
				!maps.Equal(p.HotKeys(), baseHot) || !slices.Equal(p.PushLog(), baseLog) {
				t.Fatalf("拒绝后状态被改变: view=%v pending=%v hot=%v log=%v",
					p.View(), p.Pending(), p.HotKeys(), p.PushLog())
			}
			// 判定依据：四个哨兵互不相同且可 errors.Is 判定。
			sentinels := []error{ErrInvalidThreshold, ErrEmptyKey, ErrZeroDelta, ErrEmptyBatch}
			matches := 0
			for _, s := range sentinels {
				if errors.Is(err, s) {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("错误应恰好匹配一个哨兵, 实际匹配 %d 个: %v", matches, err)
			}
			dump(t, "Reject", fmt.Sprintf("%s events=%v", c.name, c.events), err,
				fmt.Sprintf("%s；errors.Is 唯一命中；整批未生效，view/pending/hot/log 与基线逐字段一致", c.want))
		})
	}

	// 失败之后仍可正常使用。
	if err := p.FeedBatch([]Event{{Key: "a", Delta: 2}, {Key: "a", Delta: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := p.View()["a"]; got != 8 {
		t.Fatalf("非法批次后合法批次应正常生效, a=%d", got)
	}
	if !p.HotKeys()["a"] {
		t.Fatalf("a 同批出现两次，应成为热键")
	}
	dump(t, "Reject-恢复", "batch=[a+2,a+1]",
		fmt.Sprintf("view=%v hot=%v", p.View(), p.HotKeys()),
		"拒绝后实例仍可继续使用：a 缓冲 5+2+1=8 在批末随晋升冲刷")

	// FlushKey 空键同样被拒绝且无副作用。
	viewBefore := p.View()
	if _, err := p.FlushKey(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("FlushKey(\"\") err=%v, want ErrEmptyKey", err)
	}
	if !maps.Equal(p.View(), viewBefore) {
		t.Fatalf("FlushKey 空键不应改变状态: %v != %v", p.View(), viewBefore)
	}

	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestGroupSumEquivalence 全量冲刷后与批量分组求和逐键一致，重放精确重建视图。
func TestGroupSumEquivalence(t *testing.T) {
	p, _ := New(2)
	batches := [][]Event{
		{{Key: "x", Delta: 3}, {Key: "y", Delta: -1}},
		{{Key: "x", Delta: -2}, {Key: "z", Delta: 4}, {Key: "y", Delta: 6}},
		{{Key: "z", Delta: -4}, {Key: "x", Delta: 10}},
	}
	want := map[string]int64{}
	for i, b := range batches {
		if err := p.FeedBatch(b); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		for _, e := range b {
			want[e.Key] += e.Delta
		}
		// 冲刷前不变量同样成立：view + pending == 分组求和。
		if err := p.Verify(); err != nil {
			t.Fatalf("batch %d 后自检失败: %v", i, err)
		}
	}
	dump(t, "GroupSum-冲刷前", "3 个批次已喂入",
		fmt.Sprintf("view=%v pending=%v groupSum=%v", p.View(), p.Pending(), want),
		"全局视图不含缓冲；view+pending 逐键等于分组求和（Verify 已校验）")

	p.FlushAll()
	if got := p.View(); !maps.Equal(got, want) {
		t.Fatalf("全量冲刷后视图 %v != 分组求和 %v", got, want)
	}
	if got := p.Replay(); !maps.Equal(got, p.View()) {
		t.Fatalf("重放视图 %v != 当前视图 %v", got, p.View())
	}
	if len(p.Pending()) != 0 || len(p.HotKeys()) != 0 {
		t.Fatalf("FlushAll 后缓冲与热键集合应为空: pending=%v hot=%v", p.Pending(), p.HotKeys())
	}
	dump(t, "GroupSum-冲刷后", "FlushAll()",
		fmt.Sprintf("view=%v groupSum=%v replay=%v", p.View(), want, p.Replay()),
		"全量冲刷后逐键等于批量分组求和；推送记录从空重放精确重建视图")
}

// TestConcurrentReadsConsistent 并发只读：反复读取同一已喂满实例，结果逐字段一致。
func TestConcurrentReadsConsistent(t *testing.T) {
	p, _ := New(3)
	// 构造同时含有全局视图、本地缓冲与热键的“喂满”实例。
	if err := p.FeedBatch([]Event{ // a 达到阈值 -> 推送 +9 且热键
		{Key: "a", Delta: 2}, {Key: "a", Delta: 3}, {Key: "a", Delta: 4},
		{Key: "b", Delta: 5}, // b 留在缓冲
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	fixedView, fixedPending, fixedHot, fixedLog := p.View(), p.Pending(), p.HotKeys(), p.PushLog()

	const readers = 16
	const iterations = 200
	var wg sync.WaitGroup
	errCh := make(chan error, readers)
	wg.Add(readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				v, pend, hot, replay, log := p.View(), p.Pending(), p.HotKeys(), p.Replay(), p.PushLog()
				if !maps.Equal(v, fixedView) || !maps.Equal(pend, fixedPending) ||
					!maps.Equal(hot, fixedHot) || !slices.Equal(log, fixedLog) || !maps.Equal(replay, fixedView) {
					errCh <- fmt.Errorf("读到不一致快照: view=%v pending=%v hot=%v replay=%v log=%v",
						v, pend, hot, replay, log)
					return
				}
				if err := p.Verify(); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// 并发只读期间不应有任何状态漂移。
	if !maps.Equal(p.View(), fixedView) || !maps.Equal(p.Pending(), fixedPending) ||
		!maps.Equal(p.HotKeys(), fixedHot) || !slices.Equal(p.PushLog(), fixedLog) {
		t.Fatalf("并发只读结束后状态漂移: view=%v pending=%v hot=%v log=%v",
			p.View(), p.Pending(), p.HotKeys(), p.PushLog())
	}
	dump(t, "ConcurrentRead",
		fmt.Sprintf("%d readers x %d iterations", readers, iterations),
		fmt.Sprintf("view=%v pending=%v hot=%v log=%v", fixedView, fixedPending, fixedHot, fixedLog),
		"所有 reader 反复读到的 view/pending/hot/log/replay 逐字段一致，Verify 恒通过，-race 无告警")
}

// TestConcurrentReadersWithWriters 并发读与少量顺序写交错：任何读取时刻自检必须成立。
func TestConcurrentReadersWithWriters(t *testing.T) {
	p, _ := New(4)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { // 读者：全程反复读取并自检
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// Verify 与 Replay 各自都是单次持锁的一致性快照；
				// 跨调用比较可能被并发写插入，不属于读不一致。
				if err := p.Verify(); err != nil {
					t.Errorf("并发读写期间自检失败: %v", err)
					return
				}
				_ = p.Replay()
			}
		}
	}()

	for i := 0; i < 300; i++ { // 写者：混合喂批与冲刷
		key := string(rune('a' + i%4))
		_ = p.FeedBatch([]Event{{Key: key, Delta: int64(i%6 + 1)}, {Key: "z", Delta: 1}})
		if i%50 == 49 {
			p.FlushAll()
		}
		if i%37 == 36 {
			_, _ = p.FlushKey(key)
		}
	}
	close(stop)
	wg.Wait()
	p.FlushAll()

	want := map[string]int64{}
	for i := 0; i < 300; i++ {
		key := string(rune('a' + i%4))
		want[key] += int64(i%6 + 1)
		want["z"]++
	}
	if got := p.View(); !maps.Equal(got, want) {
		t.Fatalf("并发写交错最终视图 %v != 分组求和 %v", got, want)
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	dump(t, "ConcurrentReadWrite", "300 次交错喂批 + 周期 FlushAll/FlushKey",
		fmt.Sprintf("finalView=%v groupSum=%v", p.View(), want),
		"并发读在每个时刻自检通过；全部写完成并 FlushAll 后视图逐键等于分组求和")
}
