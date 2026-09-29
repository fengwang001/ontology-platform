package window

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{Size: 10, EarlyEvery: 2, Delay: 5, LateLimit: 5, MaxWindows: 64}
}

// logBatch 打印本批事件、产出的变更日志与判定依据。
func logBatch(t *testing.T, events []Event, changes []Change) {
	t.Helper()
	for _, ev := range events {
		t.Logf("事件: key=%q time=%d", ev.Key, ev.Time)
	}
	for _, ch := range changes {
		t.Logf("变更日志: seq=%d key=%q 窗口=[%d,%d) 触发=%s 种类=%s 值=%d | 判定依据: %s",
			ch.Seq, ch.Key, ch.Start, ch.End, ch.Trigger, ch.Kind, ch.Value, basis(ch))
	}
}

func basis(ch Change) string {
	switch {
	case ch.Trigger == TriggerEarly:
		return "水位线未越过右边界，计数达到早触发阈值整数倍，快照不清零"
	case ch.Trigger == TriggerOnTime:
		return "水位线越过窗口右边界(watermark>=end)，输出最终计数，每窗口至多一次"
	case ch.Trigger == TriggerLate && ch.Kind == KindRetract:
		return "准点后迟到事件，先撤回旧值"
	case ch.Trigger == TriggerLate:
		return "准点后迟到事件，撤回后发布新值"
	}
	return ""
}

func mustCounter(t *testing.T, cfg Config) *Counter {
	t.Helper()
	c, err := NewCounter(cfg)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	return c
}

func mustAdd(t *testing.T, c *Counter, events ...Event) []Change {
	t.Helper()
	changes, err := c.AddBatch(events)
	if err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	logBatch(t, events, changes)
	return changes
}

// 早触发：计数每达阈值整数倍输出快照，且计数不清零。
func TestEarlyTriggerDoesNotReset(t *testing.T) {
	c := mustCounter(t, testConfig())
	changes := mustAdd(t, c,
		Event{Key: "k", Time: 0}, Event{Key: "k", Time: 1},
		Event{Key: "k", Time: 2}, Event{Key: "k", Time: 3},
		Event{Key: "k", Time: 4},
	)
	if len(changes) != 2 {
		t.Fatalf("期望 2 条早触发快照，实际 %d 条: %+v", len(changes), changes)
	}
	for i, want := range []int64{2, 4} {
		ch := changes[i]
		if ch.Trigger != TriggerEarly || ch.Kind != KindUpsert {
			t.Fatalf("第 %d 条应为 EARLY/UPSERT，实际 %s/%s", i, ch.Trigger, ch.Kind)
		}
		if ch.Value != want {
			t.Fatalf("第 %d 条快照值应为 %d（不清零累计），实际 %d", i, want, ch.Value)
		}
		if ch.Start != 0 || ch.End != 10 {
			t.Fatalf("窗口边界应为 [0,10)，实际 [%d,%d)", ch.Start, ch.End)
		}
	}
	v := c.View()
	if len(v.Windows) != 1 || v.Windows[0].Count != 5 {
		t.Fatalf("早触发后窗口计数应为 5（不清零），实际视图 %+v", v.Windows)
	}
	if v.Windows[0].OnTimeFired {
		t.Fatalf("水位线 %d 未越过右边界 10，不应准点触发", v.Watermark)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 准点触发：水位线越过右边界输出最终计数，且每窗口至多一次。
func TestOnTimeFiresOnce(t *testing.T) {
	cfg := testConfig()
	cfg.EarlyEvery = 100 // 关闭早触发干扰
	c := mustCounter(t, cfg)

	mustAdd(t, c, Event{Key: "k", Time: 0}, Event{Key: "k", Time: 1})
	changes := mustAdd(t, c, Event{Key: "k", Time: 15}) // 水位线 15-5=10 >= 右边界 10
	if len(changes) != 1 || changes[0].Trigger != TriggerOnTime || changes[0].Value != 2 {
		t.Fatalf("期望 1 条 ON_TIME 最终计数 2，实际 %+v", changes)
	}
	// 继续推进水位线，不得重复准点触发。
	more := mustAdd(t, c, Event{Key: "k", Time: 16}, Event{Key: "k", Time: 17})
	for _, ch := range more {
		if ch.Trigger == TriggerOnTime && ch.Start == 0 {
			t.Fatalf("窗口 [0,10) 重复准点触发: %+v", ch)
		}
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 迟到事件：准点之后、清除之前到达，先撤回旧值再发新值。
func TestLateEventRetractsThenEmits(t *testing.T) {
	cfg := testConfig()
	cfg.EarlyEvery = 100
	c := mustCounter(t, cfg)

	mustAdd(t, c, Event{Key: "k", Time: 0}, Event{Key: "k", Time: 1})
	mustAdd(t, c, Event{Key: "k", Time: 15}) // ON_TIME [0,10)=2，水位线 10

	changes := mustAdd(t, c, Event{Key: "k", Time: 3}) // 迟到：10 < 10+5
	if len(changes) != 2 {
		t.Fatalf("迟到事件应产生 撤回+新值 两条日志，实际 %+v", changes)
	}
	if changes[0].Kind != KindRetract || changes[0].Value != 2 {
		t.Fatalf("第一条应为 RETRACT 旧值 2，实际 %+v", changes[0])
	}
	if changes[1].Kind != KindUpsert || changes[1].Trigger != TriggerLate || changes[1].Value != 3 {
		t.Fatalf("第二条应为 LATE/UPSERT 新值 3，实际 %+v", changes[1])
	}

	changes = mustAdd(t, c, Event{Key: "k", Time: 9})
	if len(changes) != 2 || changes[0].Value != 3 || changes[1].Value != 4 {
		t.Fatalf("第二条迟到应先撤回 3 再发 4，实际 %+v", changes)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 清除时机：水位线越过 end+LateLimit 后状态清除，之后的事件超上限被丢弃计数。
func TestCleanupTimingAndDrop(t *testing.T) {
	cfg := testConfig()
	cfg.EarlyEvery = 100
	c := mustCounter(t, cfg)

	mustAdd(t, c, Event{Key: "k", Time: 0}, Event{Key: "k", Time: 1})
	mustAdd(t, c, Event{Key: "k", Time: 15}) // ON_TIME，水位线 10

	mustAdd(t, c, Event{Key: "k", Time: 19}) // 水位线 14 < 15，窗口未清除
	if n := len(c.View().Windows); n != 2 {
		t.Fatalf("水位线 14 未达清除点 15，应剩 2 个窗口，实际 %d", n)
	}
	late := mustAdd(t, c, Event{Key: "k", Time: 5}) // 水位线 14 < 15：仍接受
	if len(late) != 2 || late[1].Value != 3 {
		t.Fatalf("清除点之前迟到事件应被接受，实际 %+v", late)
	}

	mustAdd(t, c, Event{Key: "k", Time: 20}) // 水位线 15 >= 10+5：清除 [0,10)
	for _, w := range c.View().Windows {
		if w.Start == 0 && w.Key == "k" {
			t.Fatalf("水位线 15 越过清除点，窗口 [0,10) 应已清除: %+v", w)
		}
	}

	before := c.Dropped()
	changes := mustAdd(t, c, Event{Key: "k", Time: 5}) // 超上限：丢弃
	if len(changes) != 0 {
		t.Fatalf("超上限事件不应产生日志，实际 %+v", changes)
	}
	if got := c.Dropped(); got != before+1 {
		t.Fatalf("丢弃计数应为 %d，实际 %d", before+1, got)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 负时间戳：窗口按向下取整切分，[-10,0) 包含 -1。
func TestNegativeTimestamps(t *testing.T) {
	cfg := testConfig()
	cfg.EarlyEvery = 100
	c := mustCounter(t, cfg)

	mustAdd(t, c, Event{Key: "k", Time: -1})
	v := c.View()
	if len(v.Windows) != 1 || v.Windows[0].Start != -10 || v.Windows[0].End != 0 {
		t.Fatalf("time=-1 应落入窗口 [-10,0)，实际 %+v", v.Windows)
	}
	changes := mustAdd(t, c, Event{Key: "k", Time: 5}) // 水位线 0 >= 0：准点
	if len(changes) != 1 || changes[0].Start != -10 || changes[0].End != 0 || changes[0].Value != 1 {
		t.Fatalf("负时间戳窗口准点触发错误: %+v", changes)
	}
}

// 非法配置：逐字段可区分。
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{"窗口大小为零", func(c *Config) { c.Size = 0 }, "Size"},
		{"早触发阈值为负", func(c *Config) { c.EarlyEvery = -1 }, "EarlyEvery"},
		{"水位线延迟为负", func(c *Config) { c.Delay = -1 }, "Delay"},
		{"迟到上限为负", func(c *Config) { c.LateLimit = -1 }, "LateLimit"},
		{"窗口数上限为零", func(c *Config) { c.MaxWindows = 0 }, "MaxWindows"},
	}
	for _, tc := range cases {
		cfg := testConfig()
		tc.mutate(&cfg)
		_, err := NewCounter(cfg)
		var ce *ConfigError
		if !errors.As(err, &ce) || ce.Field != tc.field {
			t.Fatalf("%s: 期望 ConfigError{%s}，实际 %v", tc.name, tc.field, err)
		}
		t.Logf("非法配置拒绝: %s -> %v", tc.name, err)
	}
}

// 批次拒绝：空键与窗口数超限整体拒绝，状态不变，原因可区分。
func TestBatchRejectIsAtomic(t *testing.T) {
	c := mustCounter(t, testConfig())
	mustAdd(t, c, Event{Key: "k", Time: 0})
	before := c.View()
	beforeDropped := c.Dropped()

	// 空键：整批不生效。
	_, err := c.AddBatch([]Event{{Key: "a", Time: 1}, {Key: "", Time: 2}})
	var re *RejectError
	if !errors.As(err, &re) || re.Reason != ReasonEmptyKey {
		t.Fatalf("期望 EMPTY_KEY 拒绝，实际 %v", err)
	}
	t.Logf("空键拒绝: %v", err)
	if after := c.View(); !reflect.DeepEqual(before, after) {
		t.Fatalf("空键拒绝后状态应不变\n前: %+v\n后: %+v", before, after)
	}

	// 窗口数超限：上限 2，一批将创建 3 个未清除窗口。
	cfg := testConfig()
	cfg.MaxWindows = 2
	cfg.LateLimit = 100
	c2 := mustCounter(t, cfg)
	_, err = c2.AddBatch([]Event{{Key: "k", Time: 0}, {Key: "k", Time: 10}, {Key: "k", Time: 20}})
	if !errors.As(err, &re) || re.Reason != ReasonTooManyWindows {
		t.Fatalf("期望 TOO_MANY_WINDOWS 拒绝，实际 %v", err)
	}
	t.Logf("窗口数超限拒绝: %v", err)
	if v := c2.View(); len(v.Windows) != 0 || v.Changes != 0 || v.HasEvents {
		t.Fatalf("超限拒绝后状态应为空，实际 %+v", v)
	}
	if got := c2.Dropped(); got != 0 {
		t.Fatalf("超限拒绝后丢弃数应为 0，实际 %d", got)
	}
	if c.Dropped() != beforeDropped {
		t.Fatal("无关实例状态被污染")
	}
}

// 并发：查询 / 丢弃数 / 自检并发调用安全；并发读取视图逐字段相同；
// 写入并发时水位线只进不退。
func TestConcurrentAccess(t *testing.T) {
	c := mustCounter(t, testConfig())
	for i := int64(0); i < 50; i++ {
		mustAdd(t, c, Event{Key: fmt.Sprintf("k%d", i%5), Time: i})
	}

	// 纯并发读：所有视图必须逐字段相同。
	const readers = 8
	views := make([]View, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				v := c.View()
				_ = c.Dropped()
				if err := c.SelfCheck(); err != nil {
					t.Error(err)
					return
				}
				views[r] = v
			}
		}(r)
	}
	wg.Wait()
	for r := 1; r < readers; r++ {
		if !reflect.DeepEqual(views[0], views[r]) {
			t.Fatalf("并发读取视图不一致\n视图0: %+v\n视图%d: %+v", views[0], r, views[r])
		}
	}

	// 读写并发：水位线观测序列不得回退。
	stop := make(chan struct{})
	var wg2 sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			prev := int64(math.MinInt64)
			for {
				select {
				case <-stop:
					return
				default:
				}
				wm := c.View().Watermark
				if wm < prev {
					t.Errorf("水位线回退: %d -> %d", prev, wm)
					return
				}
				prev = wm
			}
		}()
	}
	for i := int64(50); i < 200; i++ {
		if _, err := c.AddBatch([]Event{{Key: "w", Time: i}}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg2.Wait()
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// recompute 独立重算：按到达顺序模拟水位线，统计各窗口最终计数与丢弃数。
func recompute(cfg Config, events []Event) (counts map[windowID]int64, dropped int64) {
	counts = make(map[windowID]int64)
	var maxTime int64
	has := false
	for _, ev := range events {
		if !has || ev.Time > maxTime {
			maxTime, has = ev.Time, true
		}
		wm := subSat(maxTime, cfg.Delay)
		r := ev.Time % cfg.Size
		if r < 0 {
			r += cfg.Size
		}
		start := ev.Time - r
		end := addSat(start, cfg.Size)
		if wm >= addSat(end, cfg.LateLimit) {
			dropped++
			continue
		}
		counts[windowID{key: ev.Key, start: start}]++
	}
	return counts, dropped
}

// 批量重算核对：随机事件流（含乱序、负时间戳、迟到、丢弃），
// 最终抬高水位线使全部窗口准点并清除，逐窗口比对最后输出值与重算值；
// 同序重放两次，变更日志必须完全一致（可复现）。
func TestCrossCheckWithRecompute(t *testing.T) {
	cfg := Config{Size: 10, EarlyEvery: 3, Delay: 7, LateLimit: 11, MaxWindows: 1000}
	rng := rand.New(rand.NewSource(42))
	keys := []string{"a", "b", "c"}
	var events []Event
	for i := 0; i < 2000; i++ {
		events = append(events, Event{
			Key: keys[rng.Intn(len(keys))],
			// 大致递增的事件时间叠加 ±20 抖动：含负时间戳、乱序、迟到与丢弃。
			Time: int64(i/8) + int64(rng.Intn(41)) - 20,
		})
	}
	// 收尾事件：抬高水位线，强制所有窗口准点并清除。
	events = append(events, Event{Key: "zz", Time: 1 << 40})

	run := func() []Change {
		c := mustCounter(t, cfg)
		var all []Change
		for i := 0; i < len(events); i += 17 {
			j := i + 17
			if j > len(events) {
				j = len(events)
			}
			changes, err := c.AddBatch(events[i:j])
			if err != nil {
				t.Fatalf("AddBatch: %v", err)
			}
			all = append(all, changes...)
		}
		if err := c.SelfCheck(); err != nil {
			t.Fatal(err)
		}
		// 除哨兵事件自身所在窗口外，其余窗口应全部准点并清除。
		for _, w := range c.View().Windows {
			if w.Key != "zz" {
				t.Fatalf("收尾后窗口应已清除，实际残留 %+v", w)
			}
		}
		// 与独立重算逐窗口核对。
		counts, dropped := recompute(cfg, events)
		if c.Dropped() != dropped {
			t.Fatalf("丢弃数不一致: 计数器 %d != 重算 %d", c.Dropped(), dropped)
		}
		lastUpsert := make(map[windowID]int64)
		for _, ch := range all {
			if ch.Kind == KindUpsert {
				lastUpsert[windowID{key: ch.Key, start: ch.Start}] = ch.Value
			}
		}
		for id, want := range counts {
			if id.key == "zz" {
				continue // 哨兵窗口未准点，无输出可比对
			}
			if got, ok := lastUpsert[id]; !ok || got != want {
				t.Fatalf("窗口 %v 最终计数不一致: 输出 %d(存在=%v) != 重算 %d", id, got, ok, want)
			}
		}
		t.Logf("重算核对通过: %d 个事件, %d 个窗口, %d 条日志, 丢弃 %d",
			len(events), len(counts), len(all), dropped)
		return all
	}

	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("同序重放两次的变更日志不一致，结果不可复现")
	}
}
