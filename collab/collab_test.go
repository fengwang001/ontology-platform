package collab

import (
	"errors"
	"testing"
)

func setOp(seq int64, field string, value, baseVer int64, group string) Op {
	return Op{Seq: seq, Type: OpSet, Field: field, Value: value, BaseVer: baseVer, Group: group}
}

func addOp(seq int64, field string, delta int64, group string) Op {
	return Op{Seq: seq, Type: OpAdd, Field: field, Delta: delta, Group: group}
}

func mustSync(t *testing.T, s *Server, client, doc string, ops []Op, now int64) []OpResult {
	t.Helper()
	res, err := s.Sync(client, doc, ops, now)
	if err != nil {
		t.Fatalf("Sync rejected: %v", err)
	}
	return res.Results
}

func expectReject(t *testing.T, err error, sentinel error) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) || !errors.Is(err, sentinel) {
		t.Fatalf("want reject %v, got %v", sentinel, err)
	}
}

func newTestServer(t *testing.T, doc string, schema map[string]Kind) *Server {
	t.Helper()
	s := NewServer()
	if err := s.Create(doc, schema); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

var basicSchema = map[string]Kind{"title": KindSet, "count": KindAdd}

// 版本 == baseVer：Set 写入成功，修订号与字段版本正确。
func TestSetBaseEqual(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	res := mustSync(t, s, "c", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)
	if res[0].Kind != ResApplied {
		t.Fatalf("got %v", res[0].Kind)
	}
	snap, _ := s.Get("d")
	if snap.Revision != 1 || snap.Fields["title"].Value != 10 || snap.Fields["title"].Version != 1 {
		t.Fatalf("snapshot = %+v", snap.Fields["title"])
	}
	if p := s.Pending("c", "d"); p != 1 {
		t.Fatalf("pending = %d", p)
	}
}

// 版本 > baseVer：值相同 => 已合并；值不同 => 冲突并带回当前值/版本。
func TestSetBaseGreaterMergedAndConflict(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c1", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)

	res := mustSync(t, s, "c2", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 2)
	if res[0].Kind != ResMerged {
		t.Fatalf("want merged, got %v", res[0].Kind)
	}
	snap, _ := s.Get("d")
	if snap.Revision != 1 || snap.Fields["title"].Version != 1 {
		t.Fatalf("merged must not bump revision: %+v", snap)
	}

	res = mustSync(t, s, "c2", "d", []Op{setOp(2, "title", 11, 0, "g1")}, 3)
	if res[0].Kind != ResConflict || res[0].Value != 10 || res[0].Version != 1 || !res[0].Present {
		t.Fatalf("conflict result = %+v", res[0])
	}
	snap, _ = s.Get("d")
	if snap.Revision != 1 || snap.Fields["title"].Value != 10 {
		t.Fatalf("conflict must not change state")
	}
}

// 版本 < baseVer => 基线超前；失败操作仍消耗序号。
func TestSetBaseAhead(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	res := mustSync(t, s, "c", "d", []Op{setOp(1, "title", 1, 5, "g1")}, 1)
	if res[0].Kind != ResAhead || res[0].Version != 0 || res[0].Present {
		t.Fatalf("ahead result = %+v", res[0])
	}
	if p := s.Pending("c", "d"); p != 1 {
		t.Fatalf("failed op must consume seq, pending=%d", p)
	}
	snap, _ := s.Get("d")
	if snap.Revision != 0 || snap.Fields["title"].Present {
		t.Fatalf("ahead must not change state")
	}
}

// 同值写入（版本==baseVer）不产生有效变更，不增加修订号。
func TestSetEqualValueNoRevision(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c1", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)
	res := mustSync(t, s, "c1", "d", []Op{setOp(2, "title", 10, 1, "g1")}, 2)
	if res[0].Kind != ResApplied {
		t.Fatalf("got %v", res[0].Kind)
	}
	snap, _ := s.Get("d")
	if snap.Revision != 1 || snap.Fields["title"].Version != 1 {
		t.Fatalf("same-value write must not bump revision")
	}
}
