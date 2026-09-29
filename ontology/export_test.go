package ontology

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// exportLog 打印单测要求的四要素：动作、所处段、发出序号、判定依据。
type exportLog struct{ t *testing.T }

func (l exportLog) logf(action, seg string, seq int64, reason string, args ...any) {
	l.t.Helper()
	detail := fmt.Sprintf(reason, args...)
	if seq < 0 {
		l.t.Logf("动作=%s 段=%s 序号=- 判定依据=%s", action, seg, detail)
	} else {
		l.t.Logf("动作=%s 段=%s 序号=%d 判定依据=%s", action, seg, seq, detail)
	}
}

type emitted struct {
	rec Record
	seg Segment
}

// nextSeqForTest 仅用于日志展示。
func (s *ExportSession) nextSeqForTest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSeq
}

// drain 反复 Next，直到暂时没有记录可取；返回严格按序发出的序列。
func drain(t *testing.T, tag string, s *ExportSession) []emitted {
	t.Helper()
	l := exportLog{t}
	var got []emitted
	for {
		rec, seg, ok, err := s.Next()
		if err != nil {
			l.logf("Next("+tag+")", "错误", -1, "返回错误 %v", err)
			t.Fatalf("unexpected Next error: %v", err)
		}
		if !ok {
			drained, _ := s.Drained()
			l.logf("Next("+tag+")", seg.String(), -1,
				"下一条待发序号=%d 的记录尚不存在，不等待不跳读；drained=%v",
				s.nextSeqForTest(), drained)
			return got
		}
		l.logf("Next("+tag+")", seg.String(), rec.Seq,
			"记录存在；待发序号 %d 与起点 split=%d 比较 => %s",
			rec.Seq, s.Split(), seg.String())
		got = append(got, emitted{rec, seg})
	}
}

// finishComplete 密封日志并结束导出，核验整段为 1..lastSeq 无缝完整。
func finishComplete(t *testing.T, log *Log, s *ExportSession, wantLast int64) Report {
	t.Helper()
	log.Seal()
	rep, err := s.End()
	if err != nil {
		t.Fatalf("End: %v (report=%+v)", err, rep)
	}
	if err := rep.VerifySeam(); err != nil {
		t.Fatalf("seam: %v", err)
	}
	last := rep.Incremental.To
	if rep.Incremental.Empty() {
		last = rep.Split
	}
	if rep.Snapshot.From != 1 || last != wantLast {
		t.Fatalf("full range = snap[%d,%d] inc[%d,%d], want 1..%d",
			rep.Snapshot.From, rep.Snapshot.To, rep.Incremental.From, rep.Incremental.To, wantLast)
	}
	return rep
}

// TestSnapshotThenIncremental 覆盖快照与增量的切分，以及导出后仍可补取增量。
func TestSnapshotThenIncremental(t *testing.T) {
	l := exportLog{t}
	log := NewLog()

	for i := 1; i <= 3; i++ {
		rec, err := log.Append(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
		if err != nil {
			t.Fatal(err)
		}
		l.logf("Append", "-", rec.Seq, "校验通过，按序分配序号")
	}

	s := NewExport(log)
	split, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	l.logf("Start", "-", split, "导出开始，起点序号=%d（快照含 <=%d，增量含 >%d）",
		split, split, split)

	got := drain(t, "snapshot", s)
	if len(got) != 3 {
		t.Fatalf("snapshot segment: want 3, got %d", len(got))
	}
	for i, e := range got {
		if e.seg != SegmentSnapshot || e.rec.Seq != int64(i+1) {
			t.Fatalf("snapshot[%d] = %+v/%s", i, e.rec, e.seg)
		}
	}

	// 此刻 seq=4 尚不存在，必须不出、不跳读。
	if _, seg, ok, err := s.Next(); ok || err != nil || seg != SegmentNone {
		t.Fatalf("Next past snapshot: ok=%v seg=%s err=%v", ok, seg, err)
	}
	l.logf("Next", "none", -1, "序号=4 尚不存在，严格按序等待，不跳读")

	for i := 4; i <= 5; i++ {
		rec, err := log.Append(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
		if err != nil {
			t.Fatal(err)
		}
		l.logf("Append(导出进行中)", "-", rec.Seq, "导出未结束，仍可分配序号")
	}

	got = append(got, drain(t, "incremental", s)...)
	if len(got) != 5 {
		t.Fatalf("total: want 5, got %d", len(got))
	}
	for i := 4; i <= 5; i++ {
		e := got[i-1]
		if e.seg != SegmentIncremental || e.rec.Seq != int64(i) {
			t.Fatalf("incremental[%d] = %+v/%s", i, e.rec, e.seg)
		}
	}

	rep := finishComplete(t, log, s, 5)
	l.logf("End", "-", -1,
		"快照=[%d,%d] 增量=[%d,%d] split=%d，无缝且覆盖 1..lastSeq",
		rep.Snapshot.From, rep.Snapshot.To, rep.Incremental.From, rep.Incremental.To, rep.Split)
}

// TestIncrementalOnlyInOrder 增量段只按序发出：seq=2 不存在时绝不跳到后续序号。
func TestIncrementalOnlyInOrder(t *testing.T) {
	l := exportLog{t}
	log := NewLog()
	if _, err := log.Append("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	s := NewExport(log)
	split, _ := s.Start()
	if split != 1 {
		t.Fatalf("split = %d, want 1", split)
	}
	drain(t, "snapshot", s)

	if _, _, ok, _ := s.Next(); ok {
		t.Fatal("seq=2 missing but Next returned a record")
	}
	l.logf("Next", "incremental", -1, "seq=2 尚不存在，即使未来 seq=3 到达也绝不跳读")

	rec, _ := log.Append("k2", "v2")
	l.logf("Append", "-", rec.Seq, "seq=2 落盘")
	got, seg, ok, _ := s.Next()
	if !ok || seg != SegmentIncremental || got.Seq != 2 {
		t.Fatalf("Next: %+v %s ok=%v", got, seg, ok)
	}

	rep := finishComplete(t, log, s, 2)
	if rep.Incremental.From != 2 || rep.Incremental.To != 2 {
		t.Fatalf("incremental range = %+v", rep.Incremental)
	}
	l.logf("End", "-", -1, "增量段仅含 [2,2]，紧接快照 [1,1]，无空洞")
}

// TestSegmentOwnershipBySeq 段归属只看序号与 split，与记录到达先后无关。
func TestSegmentOwnershipBySeq(t *testing.T) {
	l := exportLog{t}
	log := NewLog()
	log.Append("a", "1")
	log.Append("b", "2")

	s := NewExport(log)
	split, _ := s.Start()
	l.logf("Start", "-", split, "split=2：序号 1、2 永远属于快照段，无论何时被读到")

	rec3, _ := log.Append("c", "3")
	l.logf("Append", "-", rec3.Seq, "seq=3 在快照尚未读完时到达")

	got := drain(t, "lazy", s)
	if len(got) != 3 {
		t.Fatalf("want 3 emitted, got %d", len(got))
	}
	if got[0].seg != SegmentSnapshot || got[1].seg != SegmentSnapshot {
		t.Fatalf("seq 1,2 must be snapshot: %s %s", got[0].seg, got[1].seg)
	}
	if got[2].seg != SegmentIncremental || got[2].rec.Seq != 3 {
		t.Fatalf("seq 3 must be incremental, got %+v/%s", got[2].rec, got[2].seg)
	}
	l.logf("Next判定", "snapshot->incremental", 3,
		"归属依据 seq<=split=%d 为快照，否则增量；与到达顺序无关", split)

	finishComplete(t, log, s, 3)
}

// TestRejections 整体拒绝且原因可区分；失败不改变日志、序号与已发序列。
func TestRejections(t *testing.T) {
	l := exportLog{t}
	log := NewLog()

	// 空键：拒绝且不占序号。
	if _, err := log.Append("", "x"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	l.logf("Append", "-", -1, "空键，校验不通过，拒绝且不分配序号")
	rec, err := log.Append("k1", "v1")
	if err != nil || rec.Seq != 1 {
		t.Fatalf("first valid append must get seq 1, got %+v %v", rec, err)
	}
	l.logf("Append", "-", rec.Seq, "被拒写入未占序号，本条仍为 1")

	// 未开始就取 / 报告 / 结束。
	s := NewExport(log)
	if _, _, _, err := s.Next(); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("want ErrExportNotStarted, got %v", err)
	}
	if _, err := s.Report(); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("Report before start: %v", err)
	}
	if _, err := s.End(); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("End before start: %v", err)
	}
	l.logf("Next/End", "-", -1, "会话未 Start，按 ErrExportNotStarted 整体拒绝")

	s.Start()
	first, seg, ok, _ := s.Next()
	if !ok || first.Seq != 1 || seg != SegmentSnapshot {
		t.Fatalf("first next = %+v/%s/%v", first, seg, ok)
	}

	// 已开始再开始：被拒，已发序列不变。
	if _, err := s.Start(); !errors.Is(err, ErrExportAlreadyStarted) {
		t.Fatalf("restart: %v", err)
	}
	l.logf("Start", "-", -1, "重复 Start 被拒；已发序列不变，仍只发过 seq=1")
	if n := s.Emitted(); n != 1 {
		t.Fatalf("emitted after rejected restart = %d, want 1", n)
	}

	// 密封后写入被拒，序号不增长。
	log.Seal()
	if _, err := log.Append("k2", "v2"); !errors.Is(err, ErrLogSealed) {
		t.Fatalf("append after seal: %v", err)
	}
	l.logf("Append", "-", -1, "日志已密封，ErrLogSealed 拒绝，不分配序号")

	rep, err := s.End()
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	l.logf("End", "-", -1, "快照=[%d,%d] 增量为空 split=%d",
		rep.Snapshot.From, rep.Snapshot.To, rep.Split)

	// 已结束再取、再开始、再结束。
	if _, _, _, err := s.Next(); !errors.Is(err, ErrExportEnded) {
		t.Fatalf("next after end: %v", err)
	}
	if _, err := s.End(); !errors.Is(err, ErrExportEnded) {
		t.Fatalf("end again: %v", err)
	}
	if _, err := s.Start(); !errors.Is(err, ErrExportEnded) {
		t.Fatalf("start after end: %v", err)
	}
	l.logf("Next/Start/End", "-", -1, "会话已结束，统一按 ErrExportEnded 拒绝")

	if log.LastSeq() != 1 {
		t.Fatalf("LastSeq = %d, want 1（被拒写入不占序号）", log.LastSeq())
	}
}

// TestEndWhileOpen 日志未密封时 End 无法保证完整，必须拒绝（区间仍报告）。
func TestEndWhileOpen(t *testing.T) {
	log := NewLog()
	log.Append("k1", "v1")
	s := NewExport(log)
	s.Start()
	s.Next()
	rep, err := s.End()
	if !errors.Is(err, ErrLogStillOpen) {
		t.Fatalf("want ErrLogStillOpen, got %v", err)
	}
	if rep.Snapshot.To != 1 {
		t.Fatalf("report should still describe emitted range: %+v", rep)
	}

	// 已密封但快照段未读满 1..split：无缝核验报 ErrSeam。
	log2 := NewLog()
	log2.Append("a", "1")
	log2.Append("b", "2")
	s2 := NewExport(log2)
	s2.Start()
	log2.Seal()
	if _, err := s2.End(); !errors.Is(err, ErrSeam) {
		t.Fatalf("partial snapshot end: want ErrSeam, got %v", err)
	}

	// 已密封、快照读完但增量落后：末条不连续，报 ErrSeam。
	log3 := NewLog()
	log3.Append("a", "1")
	s3 := NewExport(log3)
	s3.Start()
	s3.Next()
	log3.Append("b", "2") // seq=2 属于增量，未读
	log3.Seal()
	if _, err := s3.End(); !errors.Is(err, ErrSeam) {
		t.Fatalf("lagged incremental end: want ErrSeam, got %v", err)
	}
}

// TestExportLimit 未结束导出数超限：Start 整体拒绝，不登记、不影响会话。
func TestExportLimit(t *testing.T) {
	l := exportLog{t}
	log := NewLog()
	log.SetMaxActiveExports(2)
	log.Append("k1", "v1")

	s1 := NewExport(log)
	s2 := NewExport(log)
	s3 := NewExport(log)
	if _, err := s1.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Start(); !errors.Is(err, ErrExportLimitExceeded) {
		t.Fatalf("want ErrExportLimitExceeded, got %v", err)
	}
	l.logf("Start", "-", -1, "未结束会话数已达上限 2，整体拒绝，会话未登记")
	if _, _, _, err := s3.Next(); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("rejected session must remain not-started: %v", err)
	}

	log.Seal()
	drain(t, "s1", s1)
	if _, err := s1.End(); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Start(); err != nil {
		t.Fatalf("slot released after End, Start should succeed: %v", err)
	}
	l.logf("Start", "-", 1, "s1 结束后名额释放，s3 开始，split 仍为当时起点 1")
	drain(t, "s2", s2)
	if _, err := s2.End(); err != nil {
		t.Fatal(err)
	}
	drain(t, "s3", s3)
	if _, err := s3.End(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentSessionsReproducible 多会话并发推进且与写入并发；
// 各自独立完整导出的结果必须逐字段相同。
func TestConcurrentSessionsReproducible(t *testing.T) {
	l := exportLog{t}
	log := NewLog()

	const writers = 4
	const perWriter = 25
	const sessions = 4

	// 先制造一段已有历史，让不同会话拿到不同 split。
	for i := 0; i < 10; i++ {
		log.Append(fmt.Sprintf("hist-%d", i), fmt.Sprintf("h%d", i))
	}

	exp := make([]*ExportSession, sessions)
	for i := range exp {
		exp[i] = NewExport(log)
		if i > 0 { // 会话错峰开始，起点序号互不相同
			log.Append(fmt.Sprintf("gap-%d", i), fmt.Sprintf("g%d", i))
		}
		if _, err := exp[i].Start(); err != nil {
			t.Fatal(err)
		}
		l.logf("Start", "-", exp[i].Split(), "并发会话 %d 在不同时刻开始，split 各不相同", i)
	}

	var writerWg, readerWg sync.WaitGroup

	// 写入与导出并发。
	for w := 0; w < writers; w++ {
		writerWg.Add(1)
		go func(id int) {
			defer writerWg.Done()
			for i := 0; i < perWriter; i++ {
				rec, err := log.Append(fmt.Sprintf("w%d-%d", id, i), fmt.Sprintf("v%d-%d", id, i))
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				_ = rec
			}
		}(w)
	}

	results := make([][]Record, sessions)
	for i := range exp {
		readerWg.Add(1)
		go func(idx int) {
			defer readerWg.Done()
			s := exp[idx]
			var out []Record
			for {
				rec, _, ok, err := s.Next()
				if err != nil {
					t.Errorf("session %d next: %v", idx, err)
					return
				}
				if ok {
					out = append(out, rec)
					continue
				}
				// 未密封时“暂时追平”不等于结束，必须继续轮询；
				// 密封后 drained 才表示全部记录已发完。
				if !log.IsSealed() {
					runtime.Gosched()
					continue
				}
				drained, _ := s.Drained()
				if drained {
					results[idx] = out
					return
				}
				runtime.Gosched()
			}
		}(i)
	}

	// 写入全部完成后再密封：保证每个会话都能追到同一末端。
	total := int64(10 + (sessions - 1) + writers*perWriter)
	writerWg.Wait()
	log.Seal()
	l.logf("Seal", "-", total, "全部写入完成后密封，末端序号=%d", total)

	readerWg.Wait()
	for i := range exp {
		s := exp[i]
		rep, err := s.End()
		if err != nil {
			t.Fatalf("session %d end: %v", i, err)
		}
		if len(results[i]) != int(total) {
			t.Fatalf("session %d emitted %d, want %d", i, len(results[i]), total)
		}
		l.logf("End(会话)", "-", total,
			"会话 %d：快照=[%d,%d] 增量=[%d,%d] split=%d，拼接后覆盖 1..%d",
			i, rep.Snapshot.From, rep.Snapshot.To, rep.Incremental.From, rep.Incremental.To,
			rep.Split, total)
	}

	// 逐字段相同：不同 split 的会话，完整导出结果必须完全一致。
	want := results[0]
	for i := 1; i < sessions; i++ {
		got := results[i]
		if len(got) != len(want) {
			t.Fatalf("session %d length %d != %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("session %d record %d = %+v, want %+v", i, j, got[j], want[j])
			}
		}
	}
	l.logf("核对", "-", total, "全部会话导出结果逐字段相同且全局升序，每条记录恰好一次")
}

// TestReadFromHeadVerification 从头顺序读（At(1..lastSeq)）核对导出结果。
func TestReadFromHeadVerification(t *testing.T) {
	log := NewLog()
	for i := 1; i <= 6; i++ {
		log.Append(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	s := NewExport(log)
	s.Start()
	log.Append("late", "v7")

	var exported []Record
	for {
		rec, _, ok, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		exported = append(exported, rec)
	}
	finishComplete(t, log, s, 7)

	// 本地验证方法：用从头顺序读 At(1)..At(lastSeq) 逐字段比对。
	last := log.LastSeq()
	if int64(len(exported)) != last {
		t.Fatalf("exported %d records, log lastSeq %d", len(exported), last)
	}
	for seq := int64(1); seq <= last; seq++ {
		want, ok := log.At(seq)
		if !ok {
			t.Fatalf("At(%d) missing", seq)
		}
		if exported[seq-1] != want {
			t.Fatalf("seq %d: exported %+v != head-read %+v", seq, exported[seq-1], want)
		}
	}
}
