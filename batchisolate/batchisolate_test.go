package batchisolate

import (
	"fmt"
	"testing"
)

// 规格示例：R=1，a0..a4 且仅 a3 为毒丸，Calls=4。
func TestSpecExample(t *testing.T) {
	sink := newScriptSink([]string{"a3"}, nil)
	iso := mustNew(t, 1, 100, 100, sink.Write)
	res, err := iso.Submit(idsRange("a", 5))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Delivered) != "[a0 a1 a2 a4]" {
		t.Fatalf("delivered=%v", res.Delivered)
	}
	if len(res.Dead) != 1 || res.Dead[0] != (DeadLetter{"a3", Poison}) {
		t.Fatalf("dead=%v", res.Dead)
	}
	if res.Calls != 4 {
		t.Fatalf("calls=%d want 4", res.Calls)
	}
	t.Logf("输入 a0..a4 毒丸 a3 | 交付=%v 死信=%v Calls=%d | 依据: 整批永久→左半成功→右半certain直拆→a3 Poison→a4成功",
		res.Delivered, res.Dead, res.Calls)

	// 同表再提交 [a3,b,c]：a3 摘出 Known 不调用，[b,c] 一次成功，Calls=1。
	sink2 := newScriptSink(nil, nil)
	iso2 := mustNew(t, 1, 100, 100, sink2.Write)
	iso2.mergePoison([]string{"a3"})
	res2, err := iso2.Submit([]string{"a3", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res2.Delivered) != "[b c]" || len(res2.Dead) != 1 ||
		res2.Dead[0] != (DeadLetter{"a3", Known}) {
		t.Fatalf("delivered=%v dead=%v", res2.Delivered, res2.Dead)
	}
	if res2.Calls != 1 {
		t.Fatalf("calls=%d want 1", res2.Calls)
	}
}

// 已知编号摘出后批为空：不产生任何调用。
func TestKnownOnlyEmptyBatch(t *testing.T) {
	sink := newScriptSink(nil, nil)
	iso := mustNew(t, 0, 100, 10, sink.Write)
	iso.mergePoison([]string{"x", "y"})
	res, err := iso.Submit([]string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Delivered) != 0 || fmt.Sprint(deadIDs(res.Dead)) != "[x y]" {
		t.Fatalf("delivered=%v dead=%v", res.Delivered, res.Dead)
	}
	if res.Calls != 0 || sink.calls() != 0 {
		t.Fatalf("calls=%d sink=%d", res.Calls, sink.calls())
	}
	for _, d := range res.Dead {
		if d.Reason != Known {
			t.Fatalf("reason=%s want Known", d.Reason)
		}
	}
}

// 左半取 ceil(n/2) 的调用次数验证（n 奇数）。
func TestCeilSplit(t *testing.T) {
	// n=5 毒丸 a4（最右）：整批永久(1)→左3成功(2)→右[a3,a4] certain不调用
	// →左[a3] 仍以 certain=假 调用成功(3)→右[a4] 因 lok=true 而 certain，不调用直判 Poison。
	sink := newScriptSink([]string{"a4"}, nil)
	iso := mustNew(t, 0, 100, 100, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	if res.Calls != 3 {
		t.Fatalf("calls=%d want 3", res.Calls)
	}
	r := deadReasons(res.Dead)
	if r["a3"] != "" || r["a4"] != Poison {
		t.Fatalf("reasons=%v", r)
	}
	if fmt.Sprint(res.Delivered) != "[a0 a1 a2 a3]" {
		t.Fatalf("delivered=%v", res.Delivered)
	}

	// n=3 毒丸 x1：整批永久(1)→左[x0,x1]永久(2)→[x0]成功(3)→[x1]永久(4)
	// →[x1] 因左成功且本批永久而 certain 不调用直判 Poison；
	// 根右[x2] 非certain（左半整体失败 lok=false）成功(4)。
	sink2 := newScriptSink([]string{"x1"}, nil)
	iso2 := mustNew(t, 0, 100, 100, sink2.Write)
	res2, _ := iso2.Submit(idsRange("x", 3))
	if res2.Calls != 4 {
		t.Fatalf("calls=%d want 4", res2.Calls)
	}
	if fmt.Sprint(res2.Delivered) != "[x0 x2]" {
		t.Fatalf("delivered=%v", res2.Delivered)
	}
	t.Logf("ceil 划分: n=5最右毒丸 Calls=%d；n=3毒丸x1 Calls=%d", res.Calls, res2.Calls)
}

// 整批永久失败且左半成功：右半不得再整批调用。
func TestRightCertainWhenLeftSucceeds(t *testing.T) {
	sink := newScriptSink([]string{"a3"}, nil)
	iso := mustNew(t, 1, 100, 100, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	if res.Calls != 4 {
		t.Fatalf("calls=%d want 4", res.Calls)
	}
	for _, c := range sink.log {
		if fmt.Sprint(c.ids) == "[a3 a4]" {
			t.Fatalf("right half called as whole: %v", c.ids)
		}
	}
}

// 左半经瞬时重试后成功，lok 仍为真，右半按本批 perm 推断。
func TestLeftSucceedsAfterRetry(t *testing.T) {
	tr := map[string]int{setKey([]string{"a0", "a1", "a2"}): 1}
	sink := newScriptSink([]string{"a3"}, tr)
	iso := mustNew(t, 1, 100, 100, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	// 1(整批永久) + 2(左半瞬时后成功) + 右certain直拆：
	// [a3] certain=假 永久(4) Poison；[a4] 左失败非certain 成功(5)。
	if res.Calls != 5 {
		t.Fatalf("calls=%d want 5", res.Calls)
	}
	if r := deadReasons(res.Dead); r["a3"] != Poison {
		t.Fatalf("a3=%v want Poison", r["a3"])
	}
	if fmt.Sprint(res.Delivered) != "[a0 a1 a2 a4]" {
		t.Fatalf("delivered=%v", res.Delivered)
	}
	t.Logf("左半重试后成功 lok=true: Calls=%d, 右半certain直拆（右半自身不整批调用）", res.Calls)
}

// R+1 次瞬时后 perm=false，子批不推断；单条瞬时耗尽记 Exhausted 不进表。
func TestTransientExhausted(t *testing.T) {
	tr := map[string]int{setKey([]string{"x", "y"}): 2}
	sink := newScriptSink(nil, tr)
	iso := mustNew(t, 1, 100, 100, sink.Write)
	res, _ := iso.Submit([]string{"x", "y"})
	if res.Calls != 4 {
		t.Fatalf("calls=%d want 4", res.Calls)
	}
	if len(res.Dead) != 0 || fmt.Sprint(res.Delivered) != "[x y]" {
		t.Fatalf("delivered=%v dead=%v", res.Delivered, res.Dead)
	}

	tr2 := map[string]int{setKey([]string{"z"}): 2}
	sink2 := newScriptSink(nil, tr2)
	iso2 := mustNew(t, 1, 100, 100, sink2.Write)
	res2, _ := iso2.Submit([]string{"z"})
	if res2.Calls != 2 || len(res2.Dead) != 1 ||
		res2.Dead[0] != (DeadLetter{"z", Exhausted}) {
		t.Fatalf("calls=%d dead=%v", res2.Calls, res2.Dead)
	}
	if len(iso2.Known()) != 0 {
		t.Fatalf("exhausted id entered table: %v", iso2.Known())
	}
	t.Logf("瞬时耗尽: 整批perm=false Calls=%d；单条=%v 不进表", res.Calls, res2.Dead)
}

// 永久 Poison 与 certain 推断 Poison 均进表。
func TestPoisonEntersTable(t *testing.T) {
	sink := newScriptSink([]string{"p"}, nil)
	iso := mustNew(t, 0, 100, 100, sink.Write)
	res, _ := iso.Submit([]string{"p"})
	if res.Dead[0].Reason != Poison || fmt.Sprint(iso.Known()) != "[p]" {
		t.Fatalf("dead=%v known=%v", res.Dead, iso.Known())
	}

	// [g,h] 含毒丸 g：整批永久(1)，左[g] certain=假 永久(2) Poison，右[h] 非certain 成功(3)。
	sink2 := newScriptSink([]string{"g"}, nil)
	iso2 := mustNew(t, 0, 100, 100, sink2.Write)
	res2, _ := iso2.Submit([]string{"g", "h"})
	if res2.Calls != 3 || fmt.Sprint(iso2.Known()) != "[g]" {
		t.Fatalf("calls=%d known=%v", res2.Calls, iso2.Known())
	}
}

// 表满淘汰最早追加者；已在表中者不刷新；Km=0 不记录。
func TestTableEviction(t *testing.T) {
	iso := mustNew(t, 0, 2, 100, newScriptSink(nil, nil).Write)
	iso.mergePoison([]string{"a", "b"})
	iso.mergePoison([]string{"c"})
	if got := fmt.Sprint(iso.Known()); got != "[b c]" {
		t.Fatalf("after eviction known=%v want [b c]", got)
	}
	// b 已在表中：不刷新位置；追加 d 后淘汰 b。
	iso.mergePoison([]string{"b", "d"})
	if got := fmt.Sprint(iso.Known()); got != "[c d]" {
		t.Fatalf("known=%v want [c d] (existing entry must not refresh)", got)
	}

	// Km=0：Poison 不记录。
	sink := newScriptSink([]string{"q"}, nil)
	iso0 := mustNew(t, 0, 0, 100, sink.Write)
	res, _ := iso0.Submit([]string{"q"})
	if res.Dead[0].Reason != Poison || len(iso0.Known()) != 0 {
		t.Fatalf("dead=%v known=%v", res.Dead, iso0.Known())
	}
}
