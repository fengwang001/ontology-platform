package ontology

import (
	"errors"
	"testing"
)

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RejectError, got %T: %v", err, err)
	}
	return re.Reason
}

func TestRejectInvalidEvents(t *testing.T) {
	validAfter := Row{"a": "1"}
	validBefore := Row{"a": "1"}
	cases := []struct {
		name string
		e    Event
	}{
		{"未知操作", Event{Seq: 1, Key: "k", Op: "merge", After: validAfter}},
		{"insert 带前像", Event{Seq: 1, Key: "k", Op: OpInsert, Before: validBefore, After: validAfter}},
		{"insert 缺后像", Event{Seq: 1, Key: "k", Op: OpInsert}},
		{"update 缺前像", Event{Seq: 1, Key: "k", Op: OpUpdate, After: validAfter}},
		{"update 缺后像", Event{Seq: 1, Key: "k", Op: OpUpdate, Before: validBefore}},
		{"delete 缺前像", Event{Seq: 1, Key: "k", Op: OpDelete}},
		{"delete 带后像", Event{Seq: 1, Key: "k", Op: OpDelete, Before: validBefore, After: validAfter}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewStore(10)
			_, err := s.Apply([]Event{c.e})
			if got := rejectReason(t, err); got != RejectInvalidEvent {
				t.Fatalf("reason = %s, want %s", got, RejectInvalidEvent)
			}
			if s.LastSeq() != 0 || s.Snapshot().Len() != 0 || len(s.Conflicts()) != 0 {
				t.Fatalf("非法批改变了状态: seq=%d rows=%d conflicts=%d",
					s.LastSeq(), s.Snapshot().Len(), len(s.Conflicts()))
			}
		})
	}
}

func TestRejectSeqGap(t *testing.T) {
	s := NewStore(10)
	_, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"a": "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 期望 seq=2，实际 seq=3
	_, err = s.Apply([]Event{
		{Seq: 3, Key: "k2", Op: OpInsert, After: Row{"a": "1"}},
	})
	if got := rejectReason(t, err); got != RejectSeqGap {
		t.Fatalf("reason = %s, want %s", got, RejectSeqGap)
	}
	// 已处理序号仍停在 1
	if s.LastSeq() != 1 {
		t.Fatalf("LastSeq = %d, want 1", s.LastSeq())
	}

	// 批内第二条不连续：整批拒绝
	_, err = s.Apply([]Event{
		{Seq: 2, Key: "k2", Op: OpInsert, After: Row{"a": "1"}},
		{Seq: 4, Key: "k3", Op: OpInsert, After: Row{"a": "1"}},
	})
	if got := rejectReason(t, err); got != RejectSeqGap {
		t.Fatalf("批内断号 reason = %s", got)
	}
	// 第一条也不得落地
	if s.Snapshot().Len() != 1 || s.LastSeq() != 1 {
		t.Fatalf("断号批部分落地: rows=%d seq=%d", s.Snapshot().Len(), s.LastSeq())
	}

	// 回退序号也视为不连续
	_, err = s.Apply([]Event{
		{Seq: 1, Key: "k9", Op: OpInsert, After: Row{"a": "1"}},
	})
	if got := rejectReason(t, err); got != RejectSeqGap {
		t.Fatalf("回退序号 reason = %s", got)
	}
}

func TestRejectTooManyRows(t *testing.T) {
	s := NewStore(2)
	_, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"a": "1"}},
		{Seq: 2, Key: "k2", Op: OpInsert, After: Row{"a": "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 第三条插入超限，整批拒绝（前两条已在历史批中）
	bad := []Event{
		{Seq: 3, Key: "k3", Op: OpInsert, After: Row{"a": "1"}},
	}
	_, err = s.Apply(bad)
	if got := rejectReason(t, err); got != RejectTooManyRows {
		t.Fatalf("reason = %s, want %s", got, RejectTooManyRows)
	}
	if s.Snapshot().Len() != 2 || s.LastSeq() != 2 {
		t.Fatalf("超限批改变了状态: rows=%d seq=%d", s.Snapshot().Len(), s.LastSeq())
	}

	// 批内先插入到上限、再冲突、再超限插入：超限点之前的插入也必须回滚
	s2 := NewStore(1)
	_, err = s2.Apply([]Event{
		// 第一条插入成功（影子中），第二条插入超限 => 整批回滚
		{Seq: 1, Key: "a", Op: OpInsert, After: Row{"v": "1"}},
		{Seq: 2, Key: "b", Op: OpInsert, After: Row{"v": "1"}},
	})
	if got := rejectReason(t, err); got != RejectTooManyRows {
		t.Fatalf("reason = %s", got)
	}
	if s2.Snapshot().Len() != 0 || s2.LastSeq() != 0 {
		t.Fatalf("超限批部分落地: rows=%d seq=%d", s2.Snapshot().Len(), s2.LastSeq())
	}
}

func TestRejectReasonsAreDistinguishable(t *testing.T) {
	s := NewStore(1)
	_, _ = s.Apply([]Event{{Seq: 1, Key: "k", Op: OpInsert, After: Row{"a": "1"}}})

	r1 := rejectReason(t, applyOne(s, Event{Seq: 2, Key: "x", Op: "bogus"}))
	r2 := rejectReason(t, applyOne(s, Event{Seq: 9, Key: "x", Op: OpInsert, After: Row{"a": "1"}}))
	_, errTooMany := s.Apply([]Event{{Seq: 2, Key: "x", Op: OpInsert, After: Row{"a": "1"}}})
	r3 := rejectReason(t, errTooMany)

	if r1 == r2 || r2 == r3 || r1 == r3 {
		t.Fatalf("三类拒绝原因必须可区分: %s %s %s", r1, r2, r3)
	}
}

func applyOne(s *Store, e Event) error {
	_, err := s.Apply([]Event{e})
	return err
}
