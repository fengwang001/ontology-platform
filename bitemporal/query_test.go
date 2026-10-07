package bitemporal

import (
	"errors"
	"testing"
	"time"
)

type qclock struct{ base time.Time }

func newQClock() *qclock { return &qclock{base: time.Unix(0, 0).UTC()} }

func (c *qclock) at(h int) time.Time { return c.base.Add(time.Duration(h) * time.Hour) }

func (c *qclock) iv(a, b int) Interval { return Interval{From: c.at(a), To: c.at(b)} }

func queryErrKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QueryError, got %T: %v", err, err)
	}
	return qe.Kind
}

func mustT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newSeededStore(t *testing.T) (*Store, *qclock) {
	t.Helper()
	c := newQClock()
	s := NewStore()
	mustT(t, s.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendObject(ObjectRecord{ID: "B", VersionID: "B1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendObject(ObjectRecord{ID: "C", VersionID: "C1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	return s, c
}

// 修正边界：链接区间先为 [20,40)，在写入时间 50 被整体替换为 [20,60)。
// 同一 (validAt=40, asOf=30) 的查询在修正前后必须返回完全相同的结果。
func TestCorrectionBoundaryAndRepeatability(t *testing.T) {
	s, c := newSeededStore(t)
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(20, 40), WrittenAt: c.at(15)}))
	eng := NewEngine(s, nil)

	q := Query{SourceID: "A", ValidAt: c.at(40), AsOf: c.at(30), MaxDepth: 1}
	r1, err := eng.AsOf(q)
	mustT(t, err)
	if len(r1.Paths) != 0 {
		t.Fatalf("validAt=40 位于旧区间右端点（右开），路径不可见，got %d paths", len(r1.Paths))
	}
	if got := r1.Blocked[0].Combined; got != StatusNotEstablished {
		t.Fatalf("修正发生前 40 点无任何覆盖记录，want NotEstablished，got %d", got)
	}

	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L2", SourceID: "A", TargetID: "B", Valid: c.iv(20, 60), WrittenAt: c.at(50)}))

	// 修正后重放同一历史查询：原因类别从「未建立」变为「尚不可见」（未来的新
	// 知识），但路径集合必须保持稳定不变（依然为空）。
	r2, err := eng.AsOf(q)
	mustT(t, err)
	if len(r2.Paths) != 0 {
		t.Fatalf("历史 AsOf 查询必须与修正前一致，got paths=%d", len(r2.Paths))
	}
	if got := r2.Blocked[0].Combined; got != StatusNotYetVisible {
		t.Fatalf("修正记录写入更晚，历史 asOf 下应为 NotYetVisible，got %d", got)
	}

	r3, err := eng.AsOf(q)
	mustT(t, err)
	if len(r3.Paths) != len(r2.Paths) || len(r3.Blocked) != len(r2.Blocked) {
		t.Fatal("无新写入时重复查询结果必须完全相同")
	}

	r4, err := eng.AsOf(Query{SourceID: "A", ValidAt: c.at(40), AsOf: c.at(60), MaxDepth: 1})
	mustT(t, err)
	if len(r4.Paths) != 1 || r4.Paths[0].Evidence[0].LinkVersionID != "L2" {
		t.Fatalf("修正后 asOf=60 应经 L2 看见 B，got %+v", r4.Paths)
	}
}

// 链接写入时间晚于两端对象 vs 早于两端对象。
func TestLinkWriteTimeBothSides(t *testing.T) {
	c := newQClock()

	// 晚于两端对象（链接写入 50，对象写入 10），asOf=30：尚不可见。
	s := NewStore()
	mustT(t, s.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendObject(ObjectRecord{ID: "B", VersionID: "B1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 100), WrittenAt: c.at(50)}))
	eng := NewEngine(s, nil)
	r, err := eng.AsOf(Query{SourceID: "A", ValidAt: c.at(20), AsOf: c.at(30), MaxDepth: 1})
	mustT(t, err)
	if len(r.Paths) != 0 || r.Blocked[0].Combined != StatusNotYetVisible || r.Blocked[0].LinkStatus != StatusNotYetVisible {
		t.Fatalf("链接写入晚于 asOf 应为 NotYetVisible，got paths=%d blocked=%+v", len(r.Paths), r.Blocked)
	}

	// 早于两端对象（链接写入 5，对象写入 10），asOf=7 早于源最早写入 10：
	// 按固定次序报 ErrAsOfBeforeEarliestWrite。
	s2 := NewStore()
	mustT(t, s2.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s2.AppendObject(ObjectRecord{ID: "B", VersionID: "B1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s2.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 100), WrittenAt: c.at(5)}))
	_, err = NewEngine(s2, nil).AsOf(Query{SourceID: "A", ValidAt: c.at(20), AsOf: c.at(7), MaxDepth: 1})
	if queryErrKind(t, err) != ErrAsOfBeforeEarliestWrite {
		t.Fatalf("want ErrAsOfBeforeEarliestWrite，got %v", err)
	}

	// 链接写入早于两端对象，asOf=30 时三方均已落定：路径可见且证据三方齐全。
	r2, err := NewEngine(s2, nil).AsOf(Query{SourceID: "A", ValidAt: c.at(20), AsOf: c.at(30), MaxDepth: 1})
	mustT(t, err)
	if len(r2.Paths) != 1 {
		t.Fatalf("三方均落定应可见，got %d paths", len(r2.Paths))
	}
	ev := r2.Paths[0].Evidence[0]
	if ev.SourceVersionID != "A1" || ev.LinkVersionID != "L1" || ev.TargetVersionID != "B1" {
		t.Fatalf("证据须记录三方版本标识，got %+v", ev)
	}
}

// 多跳：中间一跳恰好不满足三方条件，后续截断、浅路径保留。
func TestMultiHopMiddleHopFails(t *testing.T) {
	s, c := newSeededStore(t)
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 100), WrittenAt: c.at(11)}))
	// B->C 在 validAt=50 处从未覆盖（[0,50) 右开）。
	mustT(t, s.AppendLink(LinkRecord{ID: "M", VersionID: "M1", SourceID: "B", TargetID: "C", Valid: c.iv(0, 50), WrittenAt: c.at(11)}))

	r, err := NewEngine(s, nil).AsOf(Query{SourceID: "A", ValidAt: c.at(50), AsOf: c.at(30), MaxDepth: 3})
	mustT(t, err)
	if len(r.Paths) != 1 || len(r.Paths[0].Nodes) != 2 {
		t.Fatalf("仅 1 跳浅路径应保留，got %+v", r.Paths)
	}
	var blockedM *BlockedEdge
	for i := range r.Blocked {
		if r.Blocked[i].LinkID == "M" {
			blockedM = &r.Blocked[i]
		}
	}
	if blockedM == nil || blockedM.Combined != StatusNotEstablished {
		t.Fatalf("B->C 此刻未建立，want NotEstablished，got %+v", blockedM)
	}
}

// 两类不可见原因可区分。
func TestInvisibilityCategories(t *testing.T) {
	c := newQClock()
	s := NewStore()
	mustT(t, s.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendObject(ObjectRecord{ID: "B", VersionID: "B1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	mustT(t, s.AppendObject(ObjectRecord{ID: "D", VersionID: "D1", Valid: c.iv(0, 100), WrittenAt: c.at(10)}))
	// L 对 50 从未覆盖；N 对 50 有覆盖但写入于 60。
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 50), WrittenAt: c.at(11)}))
	mustT(t, s.AppendLink(LinkRecord{ID: "N", VersionID: "N1", SourceID: "A", TargetID: "D", Valid: c.iv(40, 80), WrittenAt: c.at(60)}))

	r, err := NewEngine(s, nil).AsOf(Query{SourceID: "A", ValidAt: c.at(50), AsOf: c.at(30), MaxDepth: 1})
	mustT(t, err)
	combined := map[string]Status{}
	for _, b := range r.Blocked {
		combined[b.LinkID] = b.Combined
	}
	if combined["L"] != StatusNotEstablished {
		t.Fatalf("L want NotEstablished，got %d", combined["L"])
	}
	if combined["N"] != StatusNotYetVisible {
		t.Fatalf("N want NotYetVisible，got %d", combined["N"])
	}
}

// 错误四类互斥、判定次序固定。
func TestErrorPrecedence(t *testing.T) {
	s, c := newSeededStore(t)
	eng := NewEngine(s, nil)

	_, err := eng.AsOf(Query{SourceID: "MISSING", ValidAt: time.Time{}, AsOf: time.Time{}, MaxDepth: 0})
	if queryErrKind(t, err) != ErrSourceNotFound {
		t.Fatalf("want ErrSourceNotFound，got %v", err)
	}

	_, err = eng.AsOf(Query{SourceID: "A", ValidAt: time.Time{}, AsOf: time.Time{}, MaxDepth: 0})
	if queryErrKind(t, err) != ErrInvalidTime {
		t.Fatalf("want ErrInvalidTime，got %v", err)
	}

	_, err = eng.AsOf(Query{SourceID: "A", ValidAt: c.at(1), AsOf: c.at(2), MaxDepth: 0})
	if queryErrKind(t, err) != ErrInvalidDepth {
		t.Fatalf("want ErrInvalidDepth，got %v", err)
	}

	_, err = eng.AsOf(Query{SourceID: "A", ValidAt: c.at(1), AsOf: c.at(5), MaxDepth: 1})
	if queryErrKind(t, err) != ErrAsOfBeforeEarliestWrite {
		t.Fatalf("want ErrAsOfBeforeEarliestWrite，got %v", err)
	}
}

// 修正写入必须严格晚于被修正记录自身的写入时间，且端点不可变。
func TestStrictlyLaterCorrection(t *testing.T) {
	s, c := newSeededStore(t)
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 10), WrittenAt: c.at(20)}))
	if err := s.AppendLink(LinkRecord{ID: "L", VersionID: "L2", SourceID: "A", TargetID: "B", Valid: c.iv(0, 20), WrittenAt: c.at(20)}); err == nil {
		t.Fatal("写入时间相等必须被拒绝")
	}
	if err := s.AppendLink(LinkRecord{ID: "L", VersionID: "L2", SourceID: "A", TargetID: "B", Valid: c.iv(0, 20), WrittenAt: c.at(19)}); err == nil {
		t.Fatal("写入时间倒退必须被拒绝")
	}
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L2", SourceID: "A", TargetID: "B", Valid: c.iv(0, 20), WrittenAt: c.at(21)}))

	if err := s.AppendLink(LinkRecord{ID: "L", VersionID: "L3", SourceID: "A", TargetID: "C", Valid: c.iv(0, 20), WrittenAt: c.at(22)}); err == nil {
		t.Fatal("改变链接端点必须被拒绝")
	}
}

// 查询日志须记录输入参数、返回路径集合与每条路径的三方记录标识。
func TestQueryLogging(t *testing.T) {
	s, c := newSeededStore(t)
	mustT(t, s.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 100), WrittenAt: c.at(11)}))
	logger := NewMemoryLogger()
	eng := NewEngine(s, logger)

	q := Query{SourceID: "A", ValidAt: c.at(50), AsOf: c.at(30), MaxDepth: 1}
	_, err := eng.AsOf(q)
	mustT(t, err)

	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 log entry，got %d", len(entries))
	}
	e := entries[0]
	if e.Query != q || e.Err != nil {
		t.Fatalf("日志须记录输入参数，got %+v err=%v", e.Query, e.Err)
	}
	if e.Result == nil || len(e.Result.Paths) != 1 {
		t.Fatalf("日志须记录返回路径集合，got %+v", e.Result)
	}
	ev := e.Result.Paths[0].Evidence[0]
	if ev.SourceID != "A" || ev.SourceVersionID != "A1" ||
		ev.LinkID != "L" || ev.LinkVersionID != "L1" ||
		ev.TargetID != "B" || ev.TargetVersionID != "B1" {
		t.Fatalf("日志须记录每条路径的三方记录标识，got %+v", ev)
	}

	// 校验失败也须入日志。
	_, err = eng.AsOf(Query{SourceID: "A"})
	if err == nil {
		t.Fatal("非法查询应当返回错误")
	}
	if len(logger.Entries()) != 2 {
		t.Fatal("失败查询也必须记录日志")
	}
}
