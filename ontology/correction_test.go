package ontology

import "testing"

// TestCorrectionOverride 订正覆盖原记录的重建影响，但原记录原样保留。
func TestCorrectionOverride(t *testing.T) {
	L := testLogger{t}
	store, exec := newSeeded(t, "A")

	r1, err := exec.ExecuteAction("a1", map[string]string{"A": "wrong"}, false)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := exec.ExecuteAction("a2", map[string]string{"A": "later"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// 订正 seq=1（其值后来已被 seq=2 覆盖，故不改变「当前值」，
	// 但 seq=1 时刻的历史重建必须采用订正后的正确值）。
	corr, err := exec.Correct("fix-a1", r1.Seq, map[string]string{"A": "right"})
	if err != nil {
		t.Fatal(err)
	}
	L.log("输入: seq1=wrong, seq2=later, seq3 订正 seq1=right")
	L.log("输出: 订正记录 seq=%d correctionOf=%d changes=%+v", corr.Seq, corr.CorrectionOf, corr.Changes)

	// 原记录原样保留：seq1 的 After 仍是 wrong，且物理存在。
	orig, ok := store.Get(r1.Seq)
	if !ok {
		t.Fatal("原记录必须保留")
	}
	L.log("依据: 原 seq1 未被物理替换，After=%q（仍为 wrong）；seq3 为 CORRECTION", orig.Changes[0].After)
	if orig.Changes[0].After != "wrong" || orig.Kind != KindAction {
		t.Fatalf("原记录不得被替换: %+v", orig)
	}

	// 订正后历史：seq1 时刻重建值应为 right；当前值仍为 later。
	at1 := NewReplayer(store).StateAt(1)
	at3 := NewReplayer(store).StateAt(3)
	L.log("依据: 订正后 StateAt(1)=%s（期望 right），StateAt(3)=%s（期望 later）",
		stateString(at1), stateString(at3))
	mustState(t, at1, map[string]string{"A": "right"}, "订正覆盖 seq1 历史")
	mustState(t, at3, map[string]string{"A": "later"}, "当前值不被旧订正影响")

	// 对当前最新动作的订正应即时修正活状态。
	corr2, err := exec.Correct("fix-a2", r2.Seq, map[string]string{"A": "later-fixed"})
	if err != nil {
		t.Fatal(err)
	}
	at4 := NewReplayer(store).StateAt(corr2.Seq)
	L.log("依据: 订正最新 seq2 后 StateAt(%d)=%s（期望 later-fixed）", corr2.Seq, stateString(at4))
	mustState(t, at4, map[string]string{"A": "later-fixed"}, "订正最新动作修正当前值")

	// 同一原记录可被多次订正，最新订正胜出。
	_, err = exec.Correct("fix-a1-again", r1.Seq, map[string]string{"A": "right-v2"})
	if err != nil {
		t.Fatal(err)
	}
	at1b := NewReplayer(store).StateAt(1)
	L.log("依据: 二次订正后 StateAt(1)=%s（期望 right-v2）", stateString(at1b))
	mustState(t, at1b, map[string]string{"A": "right-v2"}, "最新订正胜出")
}

// TestCorrectionParameterCases 订正参数校验边界。
func TestCorrectionParameterCases(t *testing.T) {
	_, exec := newSeeded(t, "A", "B")
	r, err := exec.ExecuteAction("a", map[string]string{"A": "1", "B": "2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Correct("empty", r.Seq, map[string]string{}); !IsInvalidInput(err) {
		t.Fatalf("空订正应非法: %v", err)
	}
	if _, err := exec.Correct("uninvolved", r.Seq, map[string]string{"X": "9"}); !IsInvalidInput(err) {
		t.Fatalf("订正未涉及实例应非法: %v", err)
	}
	// 允许只订正原动作涉及实例的子集。
	if _, err := exec.Correct("subset", r.Seq, map[string]string{"A": "1f"}); err != nil {
		t.Fatalf("订正子集应允许: %v", err)
	}
}
