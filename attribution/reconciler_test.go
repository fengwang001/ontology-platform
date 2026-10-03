package attribution

import (
	"fmt"
	"testing"
)

func logResults(t *testing.T, events []Event, results []Result, err error) {
	t.Helper()
	t.Logf("输入批次（%d 个事件）：", len(events))
	for i, e := range events {
		t.Logf("  [%d] %s", i, formatEvent(e))
	}
	if err != nil {
		t.Logf("整批拒绝：%v", err)
		return
	}
	t.Logf("输出结论（按输入次序）：")
	for i, r := range results {
		t.Logf("  [%d] %s <- %s | %s", i, formatEvent(events[i]), r.Outcome, r.Reason)
	}
}

func formatEvent(e Event) string {
	if e.Kind == Impression {
		return fmt.Sprintf("Impression(user=%q,ad=%q,t=%d,v=%d)", e.User, e.Ad, e.T, e.V)
	}
	return fmt.Sprintf("Click(user=%q,ad=%q,t=%d)", e.User, e.Ad, e.T)
}

func wantOutcomes(t *testing.T, got []Result, want []Outcome) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("结论数量不符：got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Outcome != want[i] {
			t.Fatalf("第 %d 个事件结论不符：got %s want %s", i, got[i].Outcome, want[i])
		}
	}
}

// TestSpecExample 复现题面给定的完整示例（Tu=0）。
func TestSpecExample(t *testing.T) {
	r, err := New(Config{V: 1000, C: 60, A: 300, Tmin: 2, Lk: 50, Tu: 0})
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{
		NewImpression("u", "a", 0, 1500),
		NewImpression("u", "a", 30, 2000),
		NewClick("u", "a", 40),
		NewImpression("u", "a", 60, 500),
		NewImpression("u", "a", 60, 1000),
		NewClick("u", "a", 61),
		NewImpression("u", "a", 90, 1200),
		NewClick("u", "a", 91),
		NewClick("u", "a", 92),
		NewClick("u", "a", 93),
		NewImpression("u", "a", 141, 3000),
		NewClick("u", "a", 391),
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{
		Counted, Cooldown, Attributed, NotVisible, Cooldown, Duplicate,
		Counted, TooFast, Attributed, Duplicate, Cooldown, Expired,
	})
}

// TestSpecExampleCrossTalk 复现题面 Tu=100 的串扰变体。
func TestSpecExampleCrossTalk(t *testing.T) {
	r, _ := New(Config{V: 1000, C: 60, A: 300, Tmin: 2, Lk: 50, Tu: 100})
	events := []Event{
		NewImpression("u", "a", 0, 1500),
		NewImpression("u", "a", 30, 2000),
		NewClick("u", "a", 40),
		NewImpression("u", "a", 60, 500),
		NewImpression("u", "a", 60, 1000),
		NewClick("u", "a", 61),
		NewImpression("u", "a", 90, 1200),
		NewClick("u", "a", 91),
		NewClick("u", "a", 92),
		NewClick("u", "a", 93),
		NewImpression("u", "a", 141, 3000),
		NewClick("u", "a", 391),
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{
		Counted, Cooldown, Attributed, NotVisible, Cooldown, Duplicate,
		Counted, TooFast, CrossTalk, CrossTalk, Cooldown, Expired,
	})
}

func TestVisibilityBoundary(t *testing.T) {
	r, _ := New(Config{V: 1000, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewImpression("u", "a", 0, 1000), // 恰等于 V：可见
		NewImpression("u", "a", 1, 999),  // 差 1：不可见
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Counted, NotVisible})
}

func TestCooldownBoundary(t *testing.T) {
	r, _ := New(Config{V: 0, C: 60, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewImpression("u", "a", 0, 1),  // 已计数，锚点 0
		NewImpression("u", "a", 59, 1), // 差 1：冷却
		NewImpression("u", "a", 60, 1), // 恰等于 C：已计数
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Counted, Cooldown, Counted})
}

func TestLockBoundary(t *testing.T) {
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 50, Tu: 0})
	events := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 10),         // 归因，e=60
		NewImpression("u", "a", 59, 1), // 差 1：仍锁定，冷却
		NewImpression("u", "a", 60, 1), // 恰等于 e：不再锁定，已计数
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Counted, Attributed, Cooldown, Counted})
}

func TestLockOnlyFromAttribution(t *testing.T) {
	// 过快与重复点击都不产生/延长锁定。
	r, _ := New(Config{V: 0, C: 50, A: 1_000_000_000, Tmin: 5, Lk: 50, Tu: 0})
	b1 := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 3), // 过快：不设锁定
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	wantOutcomes(t, res1, []Outcome{Counted, TooFast})
	// 无锁定：t=100 的曝光仅受冷却约束，100-0>=C 可计数（无 e）。
	b2 := []Event{NewImpression("u", "a", 100, 1)}
	res2, err := r.Submit(b2)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b2, res2, nil)
	if res2[0].Outcome != Counted {
		t.Fatalf("过快点击不应产生锁定，100 处曝光应可计数，got %s", res2[0].Outcome)
	}
	b3 := []Event{
		NewClick("u", "a", 105),         // 归因（g=5>=Tmin），e=155
		NewClick("u", "a", 110),         // 重复：不延长锁定
		NewImpression("u", "a", 152, 1), // 152<e=155：仍锁定，冷却
	}
	res3, err := r.Submit(b3)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b3, res3, nil)
	wantOutcomes(t, res3, []Outcome{Attributed, Duplicate, Cooldown})
	b4 := []Event{NewImpression("u", "a", 155, 1)} // 恰等于 e：可计数
	res4, err := r.Submit(b4)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b4, res4, nil)
	if res4[0].Outcome != Counted {
		t.Fatalf("t 恰等于 e=155 应已计数，got %s", res4[0].Outcome)
	}
}

func TestInvisibleAndCooldownKeepAnchor(t *testing.T) {
	// 冷却/不可见曝光不移动锚点；之后的点击窗口按更早的锚点起算。
	r, _ := New(Config{V: 1000, C: 60, A: 100, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewImpression("u", "a", 0, 1000),  // 锚点 0
		NewImpression("u", "a", 50, 1000), // 冷却，锚点仍为 0
		NewClick("u", "a", 100),           // g=100 恰等于 A：归因
		NewImpression("u", "a", 101, 1),   // 不可见，锚点不变
		NewClick("u", "a", 102),           // 已归因且 g=102>A：过期先于重复
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Counted, Cooldown, Attributed, NotVisible, Expired})
}

func TestAttributionWindowBoundary(t *testing.T) {
	r, _ := New(Config{V: 0, C: 0, A: 300, Tmin: 0, Lk: 0, Tu: 0})
	b1 := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 300), // g 恰等于 A：归因
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	wantOutcomes(t, res1, []Outcome{Counted, Attributed})
	b2 := []Event{
		NewImpression("u", "b", 0, 1),
		NewClick("u", "b", 301), // g=A+1：过期
	}
	res2, err := r.Submit(b2)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b2, res2, nil)
	wantOutcomes(t, res2, []Outcome{Counted, Expired})
}

func TestTminBoundary(t *testing.T) {
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 2, Lk: 0, Tu: 0})
	b1 := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 1), // g=1 差 1：过快
		NewClick("u", "a", 2), // g 恰等于 Tmin：归因
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	wantOutcomes(t, res1, []Outcome{Counted, TooFast, Attributed})
}

func TestCrossTalkBoundary(t *testing.T) {
	// t-u 恰等于 Tu 可归因，差 1 串扰；t<u（跨批、不同 ad）也串扰。
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 100})
	b1 := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 0), // 归因，u=0
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	wantOutcomes(t, res1, []Outcome{Counted, Attributed})
	b2 := []Event{
		NewImpression("u", "b", 99, 1),
		NewClick("u", "b", 99), // 99-0=99 差 1：串扰，锚点不标记
	}
	res2, err := r.Submit(b2)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b2, res2, nil)
	wantOutcomes(t, res2, []Outcome{Counted, CrossTalk})
	b3 := []Event{NewClick("u", "b", 100)} // 恰等于 Tu：归因
	res3, err := r.Submit(b3)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b3, res3, nil)
	if res3[0].Outcome != Attributed {
		t.Fatalf("t-u 恰等于 Tu 应可归因，got %s", res3[0].Outcome)
	}
	// 跨批 t<u：另一对在早于 u=100 的时刻（但不低于该对水位 100 不可能），
	// 用新对在 t=50 曝光会因水位独立而合法，点击 t=50<u 构成串扰。
	b4 := []Event{
		NewImpression("u", "c", 50, 1),
		NewClick("u", "c", 50), // t<u，50-100<100：串扰
	}
	res4, err := r.Submit(b4)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b4, res4, nil)
	wantOutcomes(t, res4, []Outcome{Counted, CrossTalk})
	// 串扰不改锚点标记：t=200 时 t-u=100 仍可归因给同一锚点。
	b5 := []Event{NewClick("u", "c", 200)}
	res5, err := r.Submit(b5)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b5, res5, nil)
	if res5[0].Outcome != Attributed {
		t.Fatalf("串扰不应标记锚点，200 应可归因，got %s", res5[0].Outcome)
	}
}

func TestTuZeroDegenerate(t *testing.T) {
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 0),
		NewImpression("u", "b", 0, 1),
		NewClick("u", "b", 0), // Tu=0 退化：同刻跨 ad 也可归因
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Counted, Attributed, Counted, Attributed})
}

func TestImpressionBeforeClickSameTime(t *testing.T) {
	// 同刻曝光先于点击：即使输入次序相反也能归因。
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewClick("u", "a", 10),
		NewImpression("u", "a", 10, 1),
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{Attributed, Counted})
}

func TestNoImpressionClick(t *testing.T) {
	r, _ := New(Config{V: 1000, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	events := []Event{
		NewClick("u", "a", 5),
		NewImpression("u", "a", 6, 1), // 不可见，不产生锚点
		NewClick("u", "a", 7),         // 仍无曝光
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{NoImpression, NotVisible, NoImpression})
}
