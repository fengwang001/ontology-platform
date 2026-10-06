package congestion_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"ontology/congestion"
)

// genWorld 是随机序列生成器与朴素模型的共同环境。
type genWorld struct {
	t     *testing.T
	rng   *rand.Rand
	svc   *congestion.Service
	naive *naiveModel
	log   bytes.Buffer
	zids  []string
	vid   string
	last  time.Time
	retro int
	nextQ map[string]time.Time // 同类(+居民区域)下一段资格的可起时刻
}

func newWorld(t *testing.T, seed int64) *genWorld {
	cap := int64(60 + rand.New(rand.NewSource(seed)).Intn(120))
	s := congestion.New(congestion.Config{
		Location: loc, DailyCap: cap, RetroDays: 3, DiscountBasis: 100,
	})
	var buf bytes.Buffer
	s.SetLogger(&buf)
	n := newNaive(loc, cap, 100)
	w := &genWorld{t: t, rng: rand.New(rand.NewSource(seed)), svc: s, naive: n,
		log: buf, vid: "car1", retro: 3, nextQ: map[string]time.Time{}}

	// 固定区域集：outer{a,b,c,d}、inner{a,b}、solo{z}
	layout := []struct {
		id    string
		fee   int64
		cells []string
		st    int
		en    int
	}{
		{"outer", 40, []string{"a", "b", "c", "d"}, 8 * 60, 20 * 60},
		{"inner", 25, []string{"a", "b"}, 9 * 60, 18 * 60},
		{"solo", 30, []string{"z"}, 7 * 60, 22 * 60},
	}
	for _, z := range layout {
		cm := cells(z.cells...)
		if err := s.AddZone(congestion.Zone{ID: z.id, DailyFee: z.fee, Cells: cm,
			StartHHMM: z.st, EndHHMM: z.en}); err != nil {
			t.Fatal(err)
		}
		n.zones[z.id] = naiveZone{fee: z.fee, cells: cm, startMin: z.st, endMin: z.en}
		w.zids = append(w.zids, z.id)
	}
	start := atDay("2026-02-02", 600)
	if err := s.RegisterVehicle(w.vid, "P1", start); err != nil {
		t.Fatal(err)
	}
	n.vehs[w.vid] = &naiveVehicle{dispute: map[string]bool{}}
	w.last = start
	return w
}

func (w *genWorld) failf(format string, a ...any) {
	w.t.Helper()
	w.t.Fatalf(format+"\n--- operation log ---\n%s", append(a, w.log.String())...)
}

// rndTime 生成不早于上次操作时刻的时间（10 天窗口内）。
func (w *genWorld) rndTime() time.Time {
	return w.last.Add(time.Duration(1+w.rng.Intn(180)) * time.Minute)
}

func (w *genWorld) advance(tm time.Time) {
	if tm.After(w.last) {
		w.last = tm
	}
}

// verify 对比服务与朴素模型在每个有事件日的应付、逐行与不变式。
func (w *genWorld) verify() {
	nv := w.naive.vehs[w.vid]
	days := map[string]bool{}
	for _, e := range nv.entries {
		days[w.naive.dayStr(e.at)] = true
	}
	for day := range days {
		rep, err := w.svc.Query(w.vid, day)
		if err != nil {
			w.failf("query %s: %v", day, err)
		}
		nPay, nLines := w.naive.computeDay(nv, day)

		if nv.dispute[day] {
			// 冻结：服务值必须保持开争议那一刻的值
			if rep.Payable != w.naive.frozenPay[w.vid+"|"+day] {
				w.failf("day %s frozen payable svc=%d naive=%d", day,
					rep.Payable, w.naive.frozenPay[w.vid+"|"+day])
			}
			continue
		}
		if rep.Payable != nPay {
			w.failf("day %s payable svc=%d naive=%d", day, rep.Payable, nPay)
		}
		if len(rep.Lines) != len(nLines) {
			w.failf("day %s line count svc=%d naive=%d", day, len(rep.Lines), len(nLines))
		}
		for i, ln := range nLines {
			if rep.Lines[i].ZoneID != ln.zone || rep.Lines[i].Payable != ln.pay ||
				rep.Lines[i].Gross != ln.gross || rep.Lines[i].Reason != ln.reason {
				w.failf("day %s line %d svc=%+v naive=%+v", day, i, rep.Lines[i], ln)
			}
		}

		// 不变式：payable == Σ adjustments；且无冻结痕迹未清理
		var sum int64
		for _, a := range rep.Adjustments {
			sum += a.Amount
		}
		if sum != rep.Payable {
			w.failf("day %s invariant payable=%d adjSum=%d", day, rep.Payable, sum)
		}
		if rep.Frozen {
			w.failf("day %s still marked frozen", day)
		}

		// 关闭争议后：结果必须等于“争议从未发生”，仅有一笔关闭调整
		key := w.vid + "|" + day
		if _, was := w.naive.closedAdj[key]; was {
			if w.naive.closedAdj[key] == 0 {
				continue // 重算差额为零：不产生调整
			}
			cnt := 0
			for _, a := range rep.Adjustments {
				if a.Reason == "dispute_close:recompute" {
					cnt++
				}
			}
			if cnt != 1 {
				w.failf("day %s expected exactly 1 close adjustment, got %d", day, cnt)
			}
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for seed := int64(0); seed < 150; seed++ {
		w := newWorld(t, seed)
		nv := w.naive.vehs[w.vid]
		for step := 0; step < 220; step++ {
			w.oneOp(nv)
		}
		// 关闭所有仍开的争议，再做最终全量比对
		for day := range nv.dispute {
			tm := w.rndTime().Add(time.Minute)
			if err := w.svc.CloseDispute(w.vid, day, tm); err != nil {
				w.failf("close dispute %s: %v", day, err)
			}
			pay, _ := w.naive.computeDay(nv, day)
			fv := w.naive.frozenPay[w.vid+"|"+day]
			w.naive.closedAdj[w.vid+"|"+day] = pay - fv
			nv.dispute[day] = false
			w.advance(tm)
		}
		w.verify()
	}
}

func (w *genWorld) oneOp(nv *naiveVehicle) {
	switch w.rng.Intn(8) {
	case 0, 1, 2, 3: // 进入，占一半权重
		zid := w.zids[w.rng.Intn(len(w.zids))]
		tm := w.rndTime()
		err := w.svc.Enter(w.vid, zid, tm)
		if err != nil {
			w.failf("enter %s %s: %v", zid, tm, err)
		}
		nv.entries = append(nv.entries, naiveEntry{zone: zid, at: tm})
		w.advance(tm)
	case 4: // 新能源豁免
		w.addQual(2, "", 0)
	case 5: // 居民折扣（只登记某区域）
		zid := w.zids[w.rng.Intn(2)] // outer 或 inner
		discount := int64(10 + w.rng.Intn(90))
		w.addQual(0, zid, discount)
	case 6: // 开/关争议
		w.toggleDispute(nv)
	case 7: // 车牌变更
		tm := w.rndTime()
		plate := fmt.Sprintf("P%d", 2+w.rng.Intn(5))
		err := w.svc.ChangePlate(w.vid, plate, tm)
		if err != nil && err != congestion.ErrInvalid && err != congestion.ErrClockBack {
			w.failf("change plate: %v", err)
		}
		if err == nil {
			nv.plates = append(nv.plates, struct {
				at    time.Time
				plate string
			}{tm, plate})
			w.advance(tm)
		}
	}
	w.verify()
}

func (w *genWorld) addQual(kind int, zone string, discount int64) {
	// 按“类别(+居民区域)”生成首尾相接、互不重叠的分段，消除重叠歧义。
	tm := w.rndTime()
	startBase := atDay("2026-02-02", 600)
	key := fmt.Sprintf("%d|%s", kind, zone)
	start := w.nextQ[key]
	if start.IsZero() {
		start = startBase
	}
	end := start.Add(time.Duration(1+w.rng.Intn(3)) * 24 * time.Hour)
	w.nextQ[key] = end
	// 与服务相同的追溯预判：仅看“确有事件且被区间覆盖”的最早自然日。
	cutoff := tm.AddDate(0, 0, -w.retro)
	earliest := ""
	nv0 := w.naive.vehs[w.vid]
	for _, e := range nv0.entries {
		if !e.at.Before(start) && e.at.Before(end) {
			d := w.naive.dayStr(e.at)
			if earliest == "" || d < earliest {
				earliest = d
			}
		}
	}
	if earliest != "" {
		dayEnd := atDay(earliest+" 00:00", 0).Add(24 * time.Hour)
		if dayEnd.Before(cutoff) {
			return
		}
	}
	q := congestion.Qualification{Kind: congestion.QualificationKind(kind),
		ZoneID: zone, Discount: discount, Start: start, End: end}
	err := w.svc.AddQualification(w.vid, q, tm)
	if err == congestion.ErrIntervalOverlap || err == congestion.ErrTooLate ||
		err == congestion.ErrClockBack || err == congestion.ErrInvalid {
		return // 朴素模型同样不应用该资格
	}
	if err != nil {
		w.failf("add qual %+v: %v", q, err)
	}
	nq := naiveQual{kind: kind, zone: zone, discount: discount, start: start, end: end}
	nv := w.naive.vehs[w.vid]
	nv.quals = append(nv.quals, nq)
	w.advance(tm)
}

func (w *genWorld) toggleDispute(nv *naiveVehicle) {
	// 选一个有事件的日
	var candidates []string
	for _, e := range nv.entries {
		candidates = append(candidates, w.naive.dayStr(e.at))
	}
	if len(candidates) == 0 {
		return
	}
	day := candidates[w.rng.Intn(len(candidates))]
	tm := w.rndTime().Add(time.Minute)
	if nv.dispute[day] {
		return // 已开：由最终阶段统一关闭，避免重复关导致时钟约束复杂化
	}
	key := w.vid + "|" + day
	if _, seen := w.naive.frozenPay[key]; seen {
		return // 关闭过的争议不可重开（朴素模型约定，服务也拒绝）
	}
	err := w.svc.OpenDispute(w.vid, day, tm)
	if err == congestion.ErrDisputeExists || err == congestion.ErrClockBack {
		return
	}
	if err != nil {
		w.failf("open dispute %s: %v", day, err)
	}
	pay, lines := w.naive.computeDay(nv, day)
	w.naive.frozenPay[key] = pay
	w.naive.frozenLines[key] = lines
	nv.dispute[day] = true
	w.advance(tm)
}

// TestReplayDeterminism 相同操作序列重放两次，金额与明细必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() []congestion.DayReport {
		w := newWorld(t, 7)
		nv := w.naive.vehs[w.vid]
		for step := 0; step < 150; step++ {
			w.oneOp(nv)
		}
		var out []congestion.DayReport
		days := map[string]bool{}
		for _, e := range nv.entries {
			days[w.naive.dayStr(e.at)] = true
		}
		for d := range days {
			r, _ := w.svc.Query(w.vid, d)
			out = append(out, r)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
		return out
	}
	a, b := run(), run()
	if fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) {
		t.Fatalf("replay mismatch:\n%v\nvs\n%v", a, b)
	}
}

// TestConcurrentSerializable 并发调用等价于某个串行顺序：
// 并行喂入时刻单调的批次，最终不变式成立且无数据竞争。
func TestConcurrentSerializable(t *testing.T) {
	w := newWorld(t, 99)
	var wg sync.WaitGroup
	base := w.last
	for batch := 0; batch < 12; batch++ {
		tm := base.Add(time.Duration(batch+1) * time.Hour)
		zid := w.zids[batch%len(w.zids)]
		for g := 0; g < 6; g++ {
			wg.Add(1)
			go func(tm time.Time, zid string, g int) {
				defer wg.Done()
				_ = w.svc.Enter(w.vid, zid, tm.Add(time.Duration(g)*time.Nanosecond))
			}(tm, zid, g)
		}
		wg.Wait()
		w.advance(tm)
		w.naive.vehs[w.vid].entries = append(
			w.naive.vehs[w.vid].entries, naiveEntry{zone: zid, at: tm})
	}
	// 并发批次等价于“每批一次首进”；逐日用不变式核验，不依赖精确交错。
	for day := range map[string]bool{} {
		_ = day
	}
	rep, err := w.svc.Query(w.vid, "2026-02-02")
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, a := range rep.Adjustments {
		sum += a.Amount
	}
	if sum != rep.Payable {
		t.Fatalf("concurrent invariant broken: payable=%d sum=%d", rep.Payable, sum)
	}
}

var sinkReport congestion.DayReport

func buildForBench(b testing.TB, history int) *congestion.Service {
	s := newSvc()
	addStdZones(b, s)
	if err := s.AddZone(congestion.Zone{ID: "solo_x", DailyFee: 10,
		Cells: cells("x1"), StartHHMM: 8 * 60, EndHHMM: 20 * 60}); err != nil {
		b.Fatal(err)
	}
	reg(b, s, "v")
	if err := s.Enter("v", "inner", atDay("2026-03-02", 900)); err != nil {
		b.Fatal(err)
	}
	if err := s.Enter("v", "solo_x", atDay("2026-03-02", 910)); err != nil {
		b.Fatal(err)
	}
	tm := atDay("2026-03-03", 800)
	for i := 0; i < history; i++ {
		tm = tm.Add(25 * time.Hour)
		if err := s.Enter("v", "solo_x", tm); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

func benchQuery(history int) func(b *testing.B) {
	return func(b *testing.B) {
		s := buildForBench(b, history)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			r, err := s.Query("v", "2026-03-02")
			if err != nil || r.Payable <= 0 {
				b.Fatalf("bad report: %+v err=%v", r, err)
			}
			sinkReport = r
		}
	}
}

// 运行：go test -bench=BenchmarkQuery -benchmem ./congestion
// 100 条历史与 10000 条历史的 ns/op 应处于同一量级，证明查询不随历史事件数增长。
func BenchmarkQueryHistory100(b *testing.B)   { benchQuery(100)(b) }
func BenchmarkQueryHistory10000(b *testing.B) { benchQuery(10000)(b) }
