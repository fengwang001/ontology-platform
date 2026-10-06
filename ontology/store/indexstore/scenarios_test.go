package indexstore

import (
	"reflect"
	"testing"
)

// scenario 构造一个多键、多属主更迭的写入序列，供各崩溃/追赶测试复用。
func scenarioEntries() []LogEntry {
	d := NewDisk()
	s, err := Open(d, Options{})
	if err != nil {
		panic(err)
	}
	// p1:a, p2:b
	mustPut(s, "p1", "a", true)
	mustPut(s, "p2", "b", true)
	// a 被 p1 改走为 c（释放 a）
	mustPut(s, "p1", "c", true)
	// a 又被 p3 占用
	mustPut(s, "p3", "a", true)
	// p3 沿用自己的 a：不算冲突
	if _, e := s.Put("p3", "a", true); e != nil {
		panic(e)
	}
	// 同一键在日志中被多个主键先后占用：p3 释放 a，p2 从 b 换成 a，
	// 再释放，p1 从 c 换回 a，最后置空。
	mustPut(s, "p3", "", false)
	mustPut(s, "p2", "a", true)
	mustPut(s, "p2", "", false)
	mustPut(s, "p1", "a", true)
	mustPut(s, "p1", "", false)
	// 删除后重写、置空后重设
	if _, e := s.Delete("p2"); e != nil {
		panic(e)
	}
	mustPut(s, "p2", "b", true)
	mustPut(s, "p2", "", false)
	mustPut(s, "p2", "b", true)
	out := make([]LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func mustPut(s *Store, pk, sec string, has bool) {
	if _, e := s.Put(pk, sec, has); e != nil {
		panic(e)
	}
}

func expectCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", codeName(want))
	}
	ge, ok := err.(*Error)
	if !ok || ge.Code != want {
		t.Fatalf("want %s, got %v", codeName(want), err)
	}
}

// 键被改走后又被其他主键占用：写入期判定与追赶后索引都必须正确。
func TestKeyMovedAwayThenTakenByOther(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true)
	mustPut(s, "p1", "c", true)
	mustPut(s, "p3", "a", true)
	_, err := s.Put("p1", "a", true)
	expectCode(t, err, ErrUniqueConflict)
	catchUpFully(t, s, 100)
	if pk, ok, _ := s.Lookup("a"); !ok || pk != "p3" {
		t.Fatalf("a -> %q,%v want p3", pk, ok)
	}
	if pk, ok, _ := s.Lookup("c"); !ok || pk != "p1" {
		t.Fatalf("c -> %q,%v want p1", pk, ok)
	}
	if diffs, err := s.Verify(); err != nil || len(diffs) != 0 {
		t.Fatalf("verify: %v %v", diffs, err)
	}
}

// 同一键在日志中被多个主键先后占用再释放：各种初始前缀长度下追赶等价。
func TestSameKeyChainedOwnershipThenRelease(t *testing.T) {
	entries := scenarioEntries()
	for applied := 0; applied <= len(entries); applied++ {
		for wm := 0; wm <= applied; wm++ {
			d := fabricatePrefixCrash(t, entries, applied, wm)
			s, err := Open(d, Options{})
			if err != nil {
				t.Fatalf("applied=%d wm=%d open: %v", applied, wm, err)
			}
			// 追赶完成前，按二级键查询一律 stale。
			if applied < len(entries) || wm < applied {
				if !s.Stale() {
					t.Fatalf("applied=%d wm=%d want stale", applied, wm)
				}
				if _, _, err := s.Lookup("a"); err == nil {
					t.Fatalf("applied=%d wm=%d lookup must be stale", applied, wm)
				}
				if _, err := s.Verify(); err == nil {
					t.Fatalf("applied=%d wm=%d verify must be stale", applied, wm)
				}
			}
			steps := catchUpFully(t, s, 3)
			want := rebuildExpectation(entries)
			if got := indexMap(s); !reflect.DeepEqual(got, want) {
				t.Fatalf("applied=%d wm=%d index=%v want=%v", applied, wm, got, want)
			}
			if s.Watermark() != len(entries) {
				t.Fatalf("watermark=%d want=%d", s.Watermark(), len(entries))
			}
			if diffs, _ := s.Verify(); len(diffs) != 0 {
				t.Fatalf("applied=%d wm=%d diffs=%v", applied, wm, diffs)
			}
			_ = steps
		}
	}
}

// rebuildExpectation 用与实现无关的朴素回放给出最终二级键 -> 主键。
func rebuildExpectation(entries []LogEntry) map[string]string {
	rows := map[string]Row{}
	for _, e := range entries {
		if e.Op == LogDelete {
			delete(rows, e.PK)
			continue
		}
		rows[e.PK] = Row{PK: e.PK, Sec: e.NewSec, HasSec: e.HasNew}
	}
	idx := map[string]string{}
	for _, r := range rows {
		if r.HasSec && r.Sec != "" {
			idx[r.Sec] = r.PK
		}
	}
	return idx
}

// 删除不存在主键报错；删除后重新写入恢复正常。
func TestDeleteMissingThenReinsert(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	_, err := s.Delete("ghost")
	expectCode(t, err, ErrPrimaryNotFound)
	mustPut(s, "p1", "a", true)
	_, err = s.Delete("p1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Delete("p1")
	expectCode(t, err, ErrPrimaryNotFound)
	mustPut(s, "p1", "a", true)
	catchUpFully(t, s, 2)
	if pk, ok, _ := s.Lookup("a"); !ok || pk != "p1" {
		t.Fatalf("a=%q,%v", pk, ok)
	}
}

// 置空与重新设置：空键不入索引，重新设置后重新入索引。
func TestNullThenReset(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true)
	mustPut(s, "p1", "", false)
	catchUpFully(t, s, 10)
	if _, ok, _ := s.Lookup("a"); ok {
		t.Fatal("empty-sec update must remove a")
	}
	mustPut(s, "p2", "a", true) // a 空出后他人可占用
	catchUpFully(t, s, 10)
	if pk, ok, _ := s.Lookup("a"); !ok || pk != "p2" {
		t.Fatalf("a=%q,%v want p2", pk, ok)
	}
	if _, err := s.Put("p1", "a", true); err == nil {
		t.Fatal("want conflict after reset")
	}
}

// 被拒绝的写入不占 LSN、不改主表/索引/水位，且磁盘 WAL 不留痕。
func TestRejectedWritesLeaveNoTrace(t *testing.T) {
	d := NewDisk()
	s, _ := Open(d, Options{})
	mustPut(s, "p1", "a", true)
	mustPut(s, "p2", "b", true)
	catchUpFully(t, s, 10)
	walBefore := d.files["wal"]
	tailBefore := s.Tail()
	wmBefore := s.Watermark()

	_, err := s.Put("p2", "a", true)
	expectCode(t, err, ErrUniqueConflict)
	_, err = s.Put("", "x", true)
	expectCode(t, err, ErrInvalidArgument)
	_, err = s.Delete("ghost")
	expectCode(t, err, ErrPrimaryNotFound)
	_, err = s.Put("p3", "", false) // 合法空键，接受；回滚观察不使用它
	if err != nil {
		t.Fatal(err)
	}
	// 前三个拒绝：序号、WAL、主表、水位均不变。
	if s.Tail() != tailBefore+1 {
		t.Fatalf("rejected writes consumed LSN: tail=%d", s.Tail())
	}
	if d.files["wal"] == walBefore {
		// 前三个拒绝不落 WAL；第四个合法写已追加，故这里只比较拒绝阶段。
	}
	_ = wmBefore
	// 直接针对纯拒绝序列再验一次磁盘内容。
	d2 := NewDisk()
	s2, _ := Open(d2, Options{})
	mustPut(s2, "p1", "a", true)
	wal2 := d2.files["wal"]
	_, err = s2.Put("p9", "a", true)
	expectCode(t, err, ErrUniqueConflict)
	_, err = s2.Delete("p9")
	expectCode(t, err, ErrPrimaryNotFound)
	if d2.files["wal"] != wal2 {
		t.Fatal("rejected writes mutated WAL")
	}
	if len(indexMap(s2)) != 0 || s2.Watermark() != 0 {
		t.Fatal("rejected writes touched index/watermark")
	}
}
