package history

import "testing"

func TestEstCeilAndWindow(t *testing.T) {
	// D=3：样本 10,11,14，est=⌈35/3⌉=12。
	s := NewStore(5, 2, 2, 3)
	if _, ok := s.Est("x"); ok {
		t.Fatal("no samples yet, Est should not be ok")
	}
	for _, ms := range []int64{10, 11, 14} {
		s.Update("x", Clean, ms)
	}
	if got, ok := s.Est("x"); !ok || got != 12 {
		t.Fatalf("Est = %d,%v want 12,true", got, ok)
	}
	// 只留最近 D 个：加入 15 后样本为 11,14,15，est=⌈40/3⌉=14。
	s.Update("x", Clean, 15)
	if got, _ := s.Est("x"); got != 14 {
		t.Fatalf("Est after trim = %d, want 14", got)
	}
}

func TestQuarantineTriggerAndRelease(t *testing.T) {
	// W=5,F=2,P=2：恰达 F 隔离，恰达 P 解除，解除后窗口清空需重新累计。
	s := NewStore(5, 2, 2, 3)
	s.Update("x", Flaky, 10)
	s.Update("x", Clean, 10)
	if s.Quarantined("x") {
		t.Fatal("1 flaky < F=2, should not quarantine")
	}
	s.Update("x", Flaky, 10)
	if !s.Quarantined("x") {
		t.Fatal("2 flaky >= F=2, should quarantine")
	}
	// 隔离期间标记不入窗口；Broken 使连续干净数归 0。
	s.Update("x", Clean, 10)
	s.Update("x", Broken, 0)
	s.Update("x", Clean, 10)
	if !s.Quarantined("x") {
		t.Fatal("clean streak reset by Broken, should still be quarantined")
	}
	s.Update("x", Clean, 10)
	if s.Quarantined("x") {
		t.Fatal("streak reached P=2, should be released")
	}
	// 解除后窗口已清空：一个 Flaky 不足以再次隔离。
	s.Update("x", Flaky, 10)
	if s.Quarantined("x") {
		t.Fatal("window cleared on release, 1 flaky should not quarantine")
	}
	s.Update("x", Flaky, 10)
	if !s.Quarantined("x") {
		t.Fatal("2 fresh flaky >= F=2, should quarantine again")
	}
}

func TestBrokenOccupiesWindowButNotFlaky(t *testing.T) {
	// W=2,F=2：Broken 占窗口位置但不算 Flaky。
	s := NewStore(2, 2, 1, 3)
	s.Update("x", Flaky, 10)
	s.Update("x", Broken, 0) // 窗口 [Flaky, Broken]
	if s.Quarantined("x") {
		t.Fatal("Broken is not Flaky, should not quarantine")
	}
	s.Update("x", Flaky, 10) // 窗口 [Broken, Flaky]，Flaky 数仍 1
	if s.Quarantined("x") {
		t.Fatal("window [Broken,Flaky] has 1 flaky, should not quarantine")
	}
	s.Update("x", Flaky, 10) // 窗口 [Flaky, Flaky]
	if !s.Quarantined("x") {
		t.Fatal("window [Flaky,Flaky] has 2 flaky, should quarantine")
	}
}

func TestWindowTrimmedToW(t *testing.T) {
	// W=2,F=2：旧 Flaky 被挤出窗口后不计数。
	s := NewStore(2, 2, 1, 3)
	s.Update("x", Flaky, 10)
	s.Update("x", Clean, 10)
	s.Update("x", Clean, 10) // 窗口 [Clean, Clean]，旧 Flaky 已挤出
	s.Update("x", Flaky, 10) // 窗口 [Clean, Flaky]
	if s.Quarantined("x") {
		t.Fatal("old flaky evicted from window, should not quarantine")
	}
}

func TestFlakyWhileQuarantinedResetsStreak(t *testing.T) {
	s := NewStore(5, 1, 2, 3)
	s.Update("x", Flaky, 10) // F=1，立即隔离
	if !s.Quarantined("x") {
		t.Fatal("F=1 flaky should quarantine")
	}
	s.Update("x", Clean, 10)
	s.Update("x", Flaky, 10) // 连续干净数归 0
	s.Update("x", Clean, 10)
	if !s.Quarantined("x") {
		t.Fatal("streak reset by Flaky, should still be quarantined")
	}
	s.Update("x", Clean, 10)
	if s.Quarantined("x") {
		t.Fatal("streak reached P=2, should be released")
	}
}
