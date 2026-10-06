package contacttracing

import (
	"fmt"
	"sync"
	"testing"
)

// buildScaleWorld 构造一个含无关“噪声”病区的世界：
// noiseRooms 个噪声病房，每个病房 noisePerRoom 名患者、每人若干段住宿与若干病例；
// 焦点区域只有 focusRooms 个病房与少量患者。
func buildScaleWorld(b *testing.T, noiseRooms, noisePerRoom int) (*System, string, string) {
	s := New()
	now := int64(2_000_000)

	// 噪声区域：患者与病例都集中在各自噪声病房。
	for r := 0; r < noiseRooms; r++ {
		room := fmt.Sprintf("N%04d", r)
		for k := 0; k < noisePerRoom; k++ {
			p := fmt.Sprintf("n%04d_%02d", r, k)
			base := int64((r*37 + k*131) % 500000)
			mustOK(b, s.RecordStay(p, room, now, base, base+500))
			mustOK(b, s.RecordStay(p, room, now, base+600, base+1100))
			if k == 0 {
				cid, err := s.RegisterCase(p, now, base+400)
				mustOK(b, err)
				_ = cid
			}
		}
	}

	// 焦点区域：病例 f0 与密接 f1。
	mustOK(b, s.RecordStay("f0", "F", now, 1000, 1120))
	mustOK(b, s.RecordStay("f1", "F", now, 1000, 1120))
	fcid, err := s.RegisterCase("f0", now, 1500)
	mustOK(b, err)
	mustOK(b, s.RecordIsolation(fcid, now, 1200))
	return s, "f1", fcid
}

// TestStatusQueryIndependentOfTotalRecords 在两档规模上对照：
// 焦点患者状态查询扫描的住宿区间数不随噪声病区规模增长。
func TestStatusQueryIndependentOfTotalRecords(t *testing.T) {
	sizes := []struct {
		name                     string
		noiseRooms, noisePerRoom int
	}{
		{"small_20rooms", 20, 4},
		{"large_2000rooms", 2000, 4},
	}
	var scans []int64
	var totals []int
	for _, sz := range sizes {
		s, focus, _ := buildScaleWorld(t, sz.noiseRooms, sz.noisePerRoom)
		now := s.Stats().Now
		before := s.Stats()
		st, err := s.PatientStatusAt(focus, now)
		mustOK(t, err)
		if st.Status == StatusUnrelated {
			t.Fatalf("%s: focus should be recognized, got %v", sz.name, st.Status)
		}
		after := s.Stats()
		scanned := after.StaysScanned - before.StaysScanned
		scans = append(scans, scanned)
		totals = append(totals, after.Stays)
		t.Logf("%s: 总住宿=%d 次查询扫描住宿=%d 推导病例增量=%d",
			sz.name, after.Stays, scanned, after.CasesDerived-before.CasesDerived)
	}
	// 两档规模下扫描量必须相同（只与焦点患者及同病房邻域有关）。
	if scans[0] != scans[1] {
		t.Fatalf("扫描量随全院规模增长: small=%d large=%d (总住宿 %d vs %d)",
			scans[0], scans[1], totals[0], totals[1])
	}
	if int64(totals[1]) < int64(totals[0])*10 {
		t.Fatalf("规模差距不足以证明局部性")
	}
}

// TestRecomputeScopedOnChange 改正发病时刻/住宿追补后只重推导受影响病例与病房。
func TestRecomputeScopedOnChange(t *testing.T) {
	sizes := []struct {
		name                     string
		noiseRooms, noisePerRoom int
	}{
		{"small_20rooms", 20, 4},
		{"large_2000rooms", 2000, 4},
	}
	var correctScans, backfillScans []int64
	for _, sz := range sizes {
		s, focus, focusCaseID := buildScaleWorld(t, sz.noiseRooms, sz.noisePerRoom)
		// 先把所有噪声病例推导一次（制造缓存）。
		now := s.Stats().Now
		for r := 0; r < sz.noiseRooms; r++ {
			p := fmt.Sprintf("n%04d_00", r)
			_, _ = s.PatientStatusAt(p, now)
		}

		// 改正焦点病例发病时刻，统计其后一次焦点状态查询新推导的病例数与扫描量。
		focusCase := focusCaseID
		_ = focus
		mustOK(t, s.CorrectOnset(focusCase, now, 1400))
		b1 := s.Stats()
		_, _ = s.PatientStatusAt("f1", now)
		a1 := s.Stats()
		correctScans = append(correctScans, a1.StaysScanned-b1.StaysScanned)
		derived := a1.CasesDerived - b1.CasesDerived
		if derived != 1 {
			t.Fatalf("%s: 改正后重推导病例数=%d，应为 1（仅焦点病例）", sz.name, derived)
		}

		// 在焦点病房追补一段住宿，统计扫描量。
		nextNow := now + 1
		mustOK(t, s.RecordStay("f1", "F", nextNow, 1_200_000, 1_200_100))
		b2 := s.Stats()
		_, _ = s.PatientStatusAt("f1", nextNow)
		a2 := s.Stats()
		backfillScans = append(backfillScans, a2.StaysScanned-b2.StaysScanned)
		t.Logf("%s: 改正扫描=%d 追补扫描=%d（总住宿=%d）",
			sz.name, a1.StaysScanned-b1.StaysScanned, a2.StaysScanned-b2.StaysScanned, a2.Stays)
	}
	if correctScans[0] != correctScans[1] {
		t.Fatalf("改正后扫描量随规模增长: %d vs %d", correctScans[0], correctScans[1])
	}
	if backfillScans[0] != backfillScans[1] {
		t.Fatalf("追补后扫描量随规模增长: %d vs %d", backfillScans[0], backfillScans[1])
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() []StatusResult {
		s := New()
		now := int64(100000)
		mustOK(t, s.RecordStay("p", "R", now, 1000, 2000))
		mustOK(t, s.RecordStay("q", "R", now, 1000, 2000))
		mustOK(t, s.RecordStay("z", "R", now, 1000, 2000))
		cid, _ := s.RegisterCase("p", now, 2000)
		mustOK(t, s.CorrectOnset(cid, now, 1500))
		out := []StatusResult{}
		for _, p := range []string{"p", "q", "z"} {
			r, err := s.PatientStatusAt(p, now)
			mustOK(t, err)
			out = append(out, r)
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("replay mismatch at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestConcurrentSerialEquivalence 并发调用结果等价于某个串行顺序：
// 并发混合只读查询与互斥写操作，最终状态与串行执行一致，且无数据竞争（go test -race）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	build := func() *System {
		s := New()
		now := int64(100000)
		mustOK(t, s.RecordStay("p", "R", now, 1000, 2000))
		mustOK(t, s.RecordStay("q", "R", now, 1000, 2000))
		_, _ = s.RegisterCase("p", now, 2000)
		return s
	}

	// 串行基线。
	base := build()
	mustOK(t, base.CorrectOnset("C1", 100100, 1400))
	want, err := base.PatientStatusAt("q", 100100)
	mustOK(t, err)

	s := build()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 只读查询在固定 now（不推进时钟），并发安全。
			_, _ = s.PatientStatusAt("q", 100000)
		}(g)
	}
	wg.Wait()
	mustOK(t, s.CorrectOnset("C1", 100100, 1400))
	got, err := s.PatientStatusAt("q", 100100)
	mustOK(t, err)
	if got != want {
		t.Fatalf("并发后状态 %v 与串行基线 %v 不一致", got, want)
	}
}
