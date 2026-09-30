package exporter

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func logEmit(t *testing.T, s *Session, action string, recs []Record, basis string) {
	t.Helper()
	for _, r := range recs {
		t.Logf("动作=%s 段=%s 发出序号=%d 键=%s 判定依据=%s", action, s.SegmentOf(r.Seq), r.Seq, r.Key, basis)
	}
	if len(recs) == 0 {
		t.Logf("动作=%s 段=- 发出序号=- 判定依据=%s", action, basis)
	}
}

func appendN(t *testing.T, l *Log, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		seq, err := l.Append(fmt.Sprintf("%s-%d", prefix, i), fmt.Sprintf("v-%d", i))
		if err != nil {
			t.Fatalf("Append 失败: %v", err)
		}
		t.Logf("动作=写入 段=- 发出序号=%d 判定依据=校验通过，分配连续序号", seq)
	}
}

// collect 循环取数直到集满 target 条；增量段记录未写入时 NextN 返回空，需继续等待。
func collect(t *testing.T, s *Session, target int) []Record {
	t.Helper()
	var all []Record
	for len(all) < target {
		recs, err := s.NextN(2)
		if err != nil {
			t.Fatalf("NextN 失败: %v", err)
		}
		logEmit(t, s, "取数", recs, "严格按序发出，未写入则等待")
		all = append(all, recs...)
	}
	return all
}

func TestSnapshotIncrementalSplit(t *testing.T) {
	l := NewLog()
	appendN(t, l, "snap", 3)

	s := l.NewSession(100)
	if err := s.Begin(); err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	t.Logf("动作=开始导出 段=- 发出序号=- 判定依据=记录起点序号=%d，快照段上界即起点", 3)

	appendN(t, l, "incr", 2)

	got := collect(t, s, 5)
	for i, r := range got {
		if r.Seq != uint64(i+1) {
			t.Fatalf("第 %d 条序号=%d，期望 %d（全局升序、恰好一次）", i, r.Seq, i+1)
		}
		wantSeg := SegmentSnapshot
		if r.Seq > 3 {
			wantSeg = SegmentIncremental
		}
		if seg := s.SegmentOf(r.Seq); seg != wantSeg {
			t.Fatalf("序号 %d 段=%s，期望 %s", r.Seq, seg, wantSeg)
		}
	}

	rep, err := s.End()
	if err != nil {
		t.Fatalf("End 失败: %v", err)
	}
	t.Logf("动作=结束导出 段=- 发出序号=- 判定依据=报告快照段[%d,%d] 增量段[%d,%d] 并核验无缝",
		rep.Snapshot.First, rep.Snapshot.Last, rep.Incremental.First, rep.Incremental.Last)
	if rep.Snapshot != (Range{First: 1, Last: 3, Count: 3}) {
		t.Fatalf("快照段区间错误: %+v", rep.Snapshot)
	}
	if rep.Incremental != (Range{First: 4, Last: 5, Count: 2}) {
		t.Fatalf("增量段区间错误: %+v", rep.Incremental)
	}
	if rep.Total != 5 {
		t.Fatalf("总数=%d，期望 5", rep.Total)
	}
}

func TestIncrementalEmittedInOrderOnly(t *testing.T) {
	l := NewLog()
	s := l.NewSession(100)
	if err := s.Begin(); err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}

	recs, err := s.NextN(3)
	if err != nil {
		t.Fatalf("NextN 失败: %v", err)
	}
	logEmit(t, s, "取数", recs, "下一条待发序号=1 的记录尚未写入，增量段不跳读、返回空")
	if len(recs) != 0 {
		t.Fatalf("空日志上取到 %d 条，期望 0", len(recs))
	}

	appendN(t, l, "a", 1)
	recs, err = s.NextN(3)
	if err != nil {
		t.Fatalf("NextN 失败: %v", err)
	}
	logEmit(t, s, "取数", recs, "序号=1 已存在则发出；序号=2 未写入则停止，不跳读")
	if len(recs) != 1 || recs[0].Seq != 1 {
		t.Fatalf("期望只发出序号 1，实际 %+v", recs)
	}

	appendN(t, l, "b", 2)
	recs, err = s.NextN(3)
	if err != nil {
		t.Fatalf("NextN 失败: %v", err)
	}
	logEmit(t, s, "取数", recs, "序号 2、3 均已写入，按序连续发出")
	if len(recs) != 2 || recs[0].Seq != 2 || recs[1].Seq != 3 {
		t.Fatalf("期望按序发出 2、3，实际 %+v", recs)
	}

	rep, err := s.End()
	if err != nil {
		t.Fatalf("End 失败: %v", err)
	}
	if rep.Snapshot.Count != 0 || rep.Incremental != (Range{First: 1, Last: 3, Count: 3}) {
		t.Fatalf("空起点会话应全部落入增量段: %+v", rep)
	}
}

func TestSegmentAssignedBySeqNotArrival(t *testing.T) {
	l := NewLog()
	appendN(t, l, "early", 3)

	s1 := l.NewSession(100)
	if err := s1.Begin(); err != nil {
		t.Fatalf("s1 Begin 失败: %v", err)
	}
	t.Logf("动作=开始导出 段=- 发出序号=- 判定依据=会话 s1 起点序号=3")

	appendN(t, l, "late", 2)

	s2 := l.NewSession(100)
	if err := s2.Begin(); err != nil {
		t.Fatalf("s2 Begin 失败: %v", err)
	}
	t.Logf("动作=开始导出 段=- 发出序号=- 判定依据=会话 s2 起点序号=5，晚于 s1")

	got1 := collect(t, s1, 5)
	got2 := collect(t, s2, 5)

	for _, r := range got1 {
		seg := s1.SegmentOf(r.Seq)
		t.Logf("动作=判定 段=%s 发出序号=%d 判定依据=s1 起点=3，序号<=3 属快照段，否则属增量段", seg, r.Seq)
	}
	if seg := s1.SegmentOf(4); seg != SegmentIncremental {
		t.Fatalf("s1 中序号 4 应属增量段，实际 %s", seg)
	}
	if seg := s2.SegmentOf(4); seg != SegmentSnapshot {
		t.Fatalf("s2 中序号 4 应属快照段，实际 %s", seg)
	}
	t.Logf("动作=判定 段=- 发出序号=4 判定依据=段归属只取决于序号与各自起点，与记录到达先后无关")

	for i := range got1 {
		if got1[i] != got2[i] {
			t.Fatalf("第 %d 条两会话不一致: %+v vs %+v", i, got1[i], got2[i])
		}
	}
	if _, err := s1.End(); err != nil {
		t.Fatalf("s1 End 失败: %v", err)
	}
	if _, err := s2.End(); err != nil {
		t.Fatalf("s2 End 失败: %v", err)
	}
}

func TestRejectionsAreDistinguishableAndAtomic(t *testing.T) {
	l := NewLog()
	appendN(t, l, "ok", 2)

	if _, err := l.Append("", "bad"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键应返回 ErrEmptyKey，实际 %v", err)
	}
	t.Logf("动作=写入 段=- 发出序号=- 判定依据=空键整体拒绝，不分配序号")
	if got := l.Len(); got != 2 {
		t.Fatalf("被拒写入改变了序号: Len=%d，期望 2", got)
	}

	s := l.NewSession(4)
	if _, err := s.NextN(1); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("未开始就取应返回 ErrExportNotStarted，实际 %v", err)
	}
	t.Logf("动作=取数 段=- 发出序号=- 判定依据=会话未开始，整体拒绝")
	if _, err := s.End(); !errors.Is(err, ErrExportNotStarted) {
		t.Fatalf("未开始就结束应返回 ErrExportNotStarted，实际 %v", err)
	}

	if err := s.Begin(); err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if err := s.Begin(); !errors.Is(err, ErrExportAlreadyStarted) {
		t.Fatalf("重复开始应返回 ErrExportAlreadyStarted，实际 %v", err)
	}

	if _, err := s.NextN(5); !errors.Is(err, ErrExportLimitExceeded) {
		t.Fatalf("超限应返回 ErrExportLimitExceeded，实际 %v", err)
	}
	t.Logf("动作=取数 段=- 发出序号=- 判定依据=请求 5 条超过剩余限额 4，整体拒绝")

	recs, err := s.NextN(4)
	if err != nil {
		t.Fatalf("拒绝后重试应不受影响: %v", err)
	}
	logEmit(t, s, "取数", recs, "失败未改变已发序列，重试仍从序号 1 开始")
	if len(recs) != 2 || recs[0].Seq != 1 || recs[1].Seq != 2 {
		t.Fatalf("拒绝后序列被污染: %+v", recs)
	}

	rep, err := s.End()
	if err != nil {
		t.Fatalf("End 失败: %v", err)
	}
	if rep.Total != 2 {
		t.Fatalf("Total=%d，期望 2", rep.Total)
	}

	if _, err := s.NextN(1); !errors.Is(err, ErrExportAlreadyEnded) {
		t.Fatalf("已结束再取应返回 ErrExportAlreadyEnded，实际 %v", err)
	}
	if err := s.Begin(); !errors.Is(err, ErrExportAlreadyEnded) {
		t.Fatalf("已结束再开始应返回 ErrExportAlreadyEnded，实际 %v", err)
	}
	if _, err := s.End(); !errors.Is(err, ErrExportAlreadyEnded) {
		t.Fatalf("已结束再结束应返回 ErrExportAlreadyEnded，实际 %v", err)
	}
	t.Logf("动作=取数/开始/结束 段=- 发出序号=- 判定依据=会话已结束，三种操作均整体拒绝")
}

func TestConcurrentExportsAreIdentical(t *testing.T) {
	l := NewLog()
	appendN(t, l, "snap", 20)

	const total = 50
	s1 := l.NewSession(total)
	s2 := l.NewSession(total)
	if err := s1.Begin(); err != nil {
		t.Fatalf("s1 Begin 失败: %v", err)
	}
	if err := s2.Begin(); err != nil {
		t.Fatalf("s2 Begin 失败: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < total-20; i++ {
			if _, err := l.Append(fmt.Sprintf("incr-%d", i), fmt.Sprintf("v-%d", i)); err != nil {
				t.Errorf("并发写入失败: %v", err)
			}
		}
	}()
	var got1, got2 []Record
	go func() {
		defer wg.Done()
		got1 = collect(t, s1, total)
	}()
	go func() {
		defer wg.Done()
		got2 = collect(t, s2, total)
	}()
	wg.Wait()

	if len(got1) != total || len(got2) != total {
		t.Fatalf("导出条数不足: %d / %d，期望 %d", len(got1), len(got2), total)
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Fatalf("第 %d 条两会话不一致: %+v vs %+v", i, got1[i], got2[i])
		}
		if got1[i].Seq != uint64(i+1) {
			t.Fatalf("第 %d 条序号=%d，期望 %d", i, got1[i].Seq, i+1)
		}
	}
	t.Logf("动作=比对 段=- 发出序号=1..%d 判定依据=并发独立导出逐字段相同且全局升序", total)

	head := l.ReadAll()
	for i := range got1 {
		if got1[i] != head[i] {
			t.Fatalf("第 %d 条与从头顺序读不一致: %+v vs %+v", i, got1[i], head[i])
		}
	}
	t.Logf("动作=核对 段=- 发出序号=1..%d 判定依据=与从头顺序读结果逐字段一致，可复现", total)

	rep1, err := s1.End()
	if err != nil {
		t.Fatalf("s1 End 失败: %v", err)
	}
	rep2, err := s2.End()
	if err != nil {
		t.Fatalf("s2 End 失败: %v", err)
	}
	if rep1 != rep2 {
		t.Fatalf("两会话报告不一致: %+v vs %+v", rep1, rep2)
	}
}

func TestEmptyLogExport(t *testing.T) {
	l := NewLog()
	s := l.NewSession(10)
	if err := s.Begin(); err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	recs, err := s.NextN(10)
	if err != nil {
		t.Fatalf("NextN 失败: %v", err)
	}
	logEmit(t, s, "取数", recs, "空日志无快照记录，增量记录也未写入")
	rep, err := s.End()
	if err != nil {
		t.Fatalf("空导出应无缝核验通过: %v", err)
	}
	if rep.Total != 0 || rep.Snapshot.Count != 0 || rep.Incremental.Count != 0 {
		t.Fatalf("空导出报告应为空: %+v", rep)
	}
	t.Logf("动作=结束导出 段=- 发出序号=- 判定依据=两段皆空，无缝核验通过")
}
