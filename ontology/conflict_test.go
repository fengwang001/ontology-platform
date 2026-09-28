package ontology

import "testing"

// seedRow 先插入一行 k1={a:1,b:2}（seq 1），返回供冲突用例复用的 store。
func seedRow(t *testing.T) *Store {
	t.Helper()
	s := NewStore(10)
	_, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"a": "1", "b": "2"}},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func TestConflictBeforeMismatchWholeRow(t *testing.T) {
	s := seedRow(t)

	// 前像整行不等：值不符
	res, err := s.Apply([]Event{
		{Seq: 2, Key: "k1", Op: OpUpdate,
			Before: Row{"a": "1", "b": "9"}, // b 应为 2
			After:  Row{"a": "1", "b": "3"}},
	})
	if err != nil {
		t.Fatalf("冲突不是错误，但返回了 error: %v", err)
	}
	if res.Applied != 0 || len(res.Conflicts) != 1 {
		t.Fatalf("result = %+v, want 0 applied 1 conflict", res)
	}
	c := res.Conflicts[0]
	if c.Kind != ConflictBeforeMismatch || c.Seq != 2 || c.Key != "k1" {
		t.Fatalf("conflict = %+v", c)
	}
	// 副本未被改变
	if r, _ := s.Snapshot().Get("k1"); r["b"] != "2" {
		t.Fatalf("冲突后副本被改变: %v", r)
	}
	// 冲突进入累计冲突日志
	if all := s.Conflicts(); len(all) != 1 || all[0].Kind != ConflictBeforeMismatch {
		t.Fatalf("累计冲突日志 = %+v", all)
	}
	// 已处理序号仍推进（事件本身合法且被消费）
	if s.LastSeq() != 2 {
		t.Fatalf("LastSeq = %d, want 2", s.LastSeq())
	}
}

func TestConflictMissingColumnVsEmptyString(t *testing.T) {
	s := seedRow(t)

	// 当前行为 {a:1,b:2}，前像缺 b 列 => 与当前行不一致 => 冲突
	res, err := s.Apply([]Event{
		{Seq: 2, Key: "k1", Op: OpUpdate,
			Before: Row{"a": "1"},
			After:  Row{"a": "1", "b": "3"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != ConflictBeforeMismatch {
		t.Fatalf("缺列应判 before_mismatch, got %+v", res.Conflicts)
	}

	// 前像把 b 显式置空串也不能匹配（空串 != 缺列，也 != "2"）
	res, err = s.Apply([]Event{
		{Seq: 3, Key: "k1", Op: OpUpdate,
			Before: Row{"a": "1", "b": ""},
			After:  Row{"a": "1", "b": "3"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != ConflictBeforeMismatch {
		t.Fatalf("空串前像应判 before_mismatch, got %+v", res.Conflicts)
	}

	// 副本始终未变
	if r, _ := s.Snapshot().Get("k1"); r["a"] != "1" || r["b"] != "2" {
		t.Fatalf("副本被改变: %v", r)
	}
}

func TestConflictRowMissing(t *testing.T) {
	for _, op := range []Op{OpUpdate, OpDelete} {
		s2 := NewStore(10)
		_, _ = s2.Apply([]Event{
			{Seq: 1, Key: "other", Op: OpInsert, After: Row{"x": "1"}},
		})
		e := Event{Seq: 2, Key: "ghost", Op: op, Before: Row{"x": "1"}}
		if op == OpUpdate {
			e.After = Row{"x": "2"}
		}
		res, err := s2.Apply([]Event{e})
		if err != nil {
			t.Fatalf("%s 行不存在应为冲突而非错误: %v", op, err)
		}
		if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != ConflictRowMissing {
			t.Fatalf("%s 行不存在应判 row_missing, got %+v", op, res.Conflicts)
		}
		if res.Applied != 0 {
			t.Fatalf("%s 冲突不应应用, applied=%d", op, res.Applied)
		}
	}
}

func TestConflictInsertRowExists(t *testing.T) {
	s := seedRow(t)
	res, err := s.Apply([]Event{
		{Seq: 2, Key: "k1", Op: OpInsert, After: Row{"a": "9"}},
	})
	if err != nil {
		t.Fatalf("插入已存在应为冲突而非错误: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != ConflictRowExists {
		t.Fatalf("应判 row_exists, got %+v", res.Conflicts)
	}
	// 现有行不被覆盖
	if r, _ := s.Snapshot().Get("k1"); r["a"] != "1" || r["b"] != "2" {
		t.Fatalf("冲突插入覆盖了现有行: %v", r)
	}
}
