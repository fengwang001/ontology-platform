package mvgc

import (
	"strings"
	"testing"
)

func mustWrite(t *testing.T, s *Store, key string, typ byte, ts int64, value string) {
	t.Helper()
	if err := s.Write(key, typ, ts, value); err != nil {
		t.Fatalf("Write(%s,%c,%d) failed: %v", key, typ, ts, err)
	}
}

func getOK(t *testing.T, s *Store, key string, ts int64, want string) {
	t.Helper()
	got, ok, err := s.Get(key, ts)
	if err != nil || !ok || got != want {
		t.Fatalf("Get(%s,%d) = %q,%v,%v want %q,true,nil", key, ts, got, ok, err, want)
	}
}

func getNone(t *testing.T, s *Store, key string, ts int64) {
	t.Helper()
	got, ok, err := s.Get(key, ts)
	if err != nil || ok {
		t.Fatalf("Get(%s,%d) = %q,%v,%v want none", key, ts, got, ok, err)
	}
}

func dump(s *Store, key string) []rec {
	n := treapFind(s.root, key)
	if n == nil {
		return nil
	}
	return append([]rec{}, n.recs...)
}

func recsStr(rs []rec) string {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte(r.typ)
		b.WriteString(itoa(r.ts))
		if r.typ == TypePut {
			b.WriteString("(" + r.value + ")")
		}
	}
	return b.String()
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func expectKind(t *testing.T, err error, kind RejectKind) {
	t.Helper()
	oe, ok := err.(*OpError)
	if !ok || oe.Kind != kind {
		t.Fatalf("err = %v, want kind %d", err, kind)
	}
}

// TestSpecExample 覆盖题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	s := New(1000)
	k := "k"
	mustWrite(t, s, k, TypePut, 5, "a")
	mustWrite(t, s, k, TypeLock, 7, "")
	mustWrite(t, s, k, TypePut, 9, "b")
	mustWrite(t, s, k, TypeDelete, 12, "")
	mustWrite(t, s, k, TypeRollback, 13, "")
	mustWrite(t, s, k, TypePut, 15, "c")

	getOK(t, s, k, 11, "b")
	getNone(t, s, k, 13)

	if err := s.SetSafePoint(10); err != nil {
		t.Fatal(err)
	}
	n, err := s.GCStep(10)
	if err != nil || n != 1 {
		t.Fatalf("GCStep = %d,%v", n, err)
	}
	if s.AccessCount() != 3 {
		t.Fatalf("access = %d want 3", s.AccessCount())
	}
	if got := recsStr(dump(s, k)); got != "P9(b) D12 R13 P15(c)" {
		t.Fatalf("after sp10: %s", got)
	}

	if err := s.SetSafePoint(13); err != nil {
		t.Fatal(err)
	}
	n, err = s.GCStep(10)
	if err != nil || n != 1 {
		t.Fatalf("GCStep = %d,%v", n, err)
	}
	if s.AccessCount() != 6 {
		t.Fatalf("access = %d want 6", s.AccessCount())
	}
	if got := recsStr(dump(s, k)); got != "P15(c)" {
		t.Fatalf("after sp13: %s", got)
	}

	getNone(t, s, k, 14)
	getOK(t, s, k, 15, "c")
	_, _, err = s.Get(k, 12)
	expectKind(t, err, ErrExpired)
}

// TestXPutKeepsX：X 为 P 时保留 X；ts==sp 参与处理。
func TestXPutKeepsX(t *testing.T) {
	s := New(100)
	mustWrite(t, s, "k", TypePut, 1, "a")
	mustWrite(t, s, "k", TypeDelete, 2, "")
	mustWrite(t, s, "k", TypeLock, 3, "")
	mustWrite(t, s, "k", TypePut, 4, "d")
	mustWrite(t, s, "k", TypeRollback, 5, "")
	mustWrite(t, s, "k", TypePut, 6, "f")
	if err := s.SetSafePoint(4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GCStep(1); err != nil {
		t.Fatal(err)
	}
	if got := recsStr(dump(s, "k")); got != "P4(d) R5 P6(f)" {
		t.Fatalf("got %s", got)
	}
	if s.AccessCount() != 4 {
		t.Fatalf("access = %d want 4 (ts 1..4)", s.AccessCount())
	}
}

// TestXDeleteRemoved：X 为 D 时连同 X 一起删除。
func TestXDeleteRemoved(t *testing.T) {
	s := New(100)
	mustWrite(t, s, "k", TypePut, 1, "a")
	mustWrite(t, s, "k", TypePut, 2, "b")
	mustWrite(t, s, "k", TypeDelete, 3, "")
	mustWrite(t, s, "k", TypePut, 8, "c")
	if err := s.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GCStep(1); err != nil {
		t.Fatal(err)
	}
	if got := recsStr(dump(s, "k")); got != "P8(c)" {
		t.Fatalf("got %s", got)
	}
	getNone(t, s, "k", 7)
	getOK(t, s, "k", 8, "c")
}

// TestLockRollbackSkippedAndFutureKept：L/R 被 Get 跳过；
// <=sp 的 L/R 先删除，>sp 的 L/R 保留。
func TestLockRollbackSkippedAndFutureKept(t *testing.T) {
	s := New(100)
	mustWrite(t, s, "k", TypePut, 1, "a")
	mustWrite(t, s, "k", TypeLock, 2, "")
	mustWrite(t, s, "k", TypeRollback, 3, "")
	mustWrite(t, s, "k", TypeDelete, 4, "")
	mustWrite(t, s, "k", TypeLock, 9, "")
	mustWrite(t, s, "k", TypeRollback, 10, "")
	getOK(t, s, "k", 2, "a")
	getOK(t, s, "k", 3, "a")
	getNone(t, s, "k", 5)
	if err := s.SetSafePoint(4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GCStep(1); err != nil {
		t.Fatal(err)
	}
	if got := recsStr(dump(s, "k")); got != "L9 R10" {
		t.Fatalf("got %s", got)
	}
}

// TestOnlyLockRollbackAndKeyDisappears：安全点内无 P/D 时只删 L/R；
// 记录删光后键消失，且本轮结束返回 0。
func TestOnlyLockRollbackAndKeyDisappears(t *testing.T) {
	s := New(100)
	mustWrite(t, s, "k", TypeLock, 1, "")
	mustWrite(t, s, "k", TypeRollback, 2, "")
	mustWrite(t, s, "k", TypeLock, 3, "")
	if err := s.SetSafePoint(3); err != nil {
		t.Fatal(err)
	}
	n, err := s.GCStep(1)
	if err != nil || n != 1 {
		t.Fatalf("GCStep = %d,%v", n, err)
	}
	if treapFind(s.root, "k") != nil {
		t.Fatal("key should disappear when all records removed")
	}
	n, err = s.GCStep(1)
	if err != nil || n != 0 {
		t.Fatalf("GCStep end = %d,%v", n, err)
	}
}

// TestCursorBatchingAndNewKeys：游标分批边界与本轮新键规则。
func TestCursorBatchingAndNewKeys(t *testing.T) {
	s := New(10000)
	for _, k := range []string{"b", "c", "d"} {
		mustWrite(t, s, k, TypePut, 10, "v")
	}
	if err := s.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	n, err := s.GCStep(2)
	if err != nil || n != 2 {
		t.Fatalf("step = %d,%v", n, err)
	}
	if s.cursor != "c" || !s.haveCur {
		t.Fatalf("cursor = %q", s.cursor)
	}
	mustWrite(t, s, "a", TypePut, 10, "v")  // 小于游标：下一轮
	mustWrite(t, s, "cc", TypePut, 10, "v") // 大于游标：本轮
	n, err = s.GCStep(10)
	if err != nil || n != 2 { // cc, d
		t.Fatalf("step = %d,%v", n, err)
	}
	n, err = s.GCStep(10)
	if err != nil || n != 0 { // 本轮结束，a 未处理
		t.Fatalf("step end = %d,%v", n, err)
	}
	if got := recsStr(dump(s, "a")); got != "P10(v)" {
		t.Fatalf("a must wait for next round, got %q", got)
	}
	if err := s.SetSafePoint(6); err != nil {
		t.Fatal(err)
	}
	n, err = s.GCStep(10)
	if err != nil || n != 5 { // 新一轮重处理全部 5 个键，a 排第一
		t.Fatalf("new round step = %d,%v", n, err)
	}
	if got := recsStr(dump(s, "a")); got != "P10(v)" {
		t.Fatalf("a = %s", got)
	}
}

// TestMidRoundSafePointReprocesses：中途推进安全点作废游标并重处理。
func TestMidRoundSafePointReprocesses(t *testing.T) {
	s := New(1000)
	mustWrite(t, s, "k", TypePut, 2, "a")
	mustWrite(t, s, "k", TypePut, 8, "b")
	if err := s.SetSafePoint(1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GCStep(10); err != nil {
		t.Fatal(err)
	}
	if s.AccessCount() != 0 {
		t.Fatalf("access = %d want 0", s.AccessCount())
	}
	if err := s.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	if s.haveCur {
		t.Fatal("cursor must be reset")
	}
	if _, err := s.GCStep(10); err != nil {
		t.Fatal(err)
	}
	if s.AccessCount() != 1 {
		t.Fatalf("access = %d want 1 (P2 reprocessed)", s.AccessCount())
	}
	// X=P(2) 为 Put，按规则保留 X：P2 仍在。
	if got := recsStr(dump(s, "k")); got != "P2(a) P8(b)" {
		t.Fatalf("got %s", got)
	}
}

// TestSnapshotConstraints：安全点恰等于最小快照 ts 允许，大 1 被拒。
func TestSnapshotConstraints(t *testing.T) {
	s := New(100)
	id, err := s.OpenSnapshot(10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSafePoint(10); err != nil {
		t.Fatalf("equal sp should be allowed: %v", err)
	}
	expectKind(t, s.SetSafePoint(11), ErrSnapshotBlocked)
	if s.SafePoint() != 10 {
		t.Fatalf("sp = %d", s.SafePoint())
	}
	if err := s.CloseSnapshot(id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSafePoint(11); err != nil {
		t.Fatalf("after close: %v", err)
	}
	expectKind(t, s.CloseSnapshot(id), ErrSnapshotNotFound)

	_, _ = s.OpenSnapshot(20)
	id2, _ := s.OpenSnapshot(15)
	expectKind(t, s.SetSafePoint(16), ErrSnapshotBlocked)
	if err := s.CloseSnapshot(id2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSafePoint(16); err != nil {
		t.Fatalf("min removed: %v", err)
	}
}

// TestRollbackRejected：安全点回退被拒且状态不变。
func TestRollbackRejected(t *testing.T) {
	s := New(100)
	if err := s.SetSafePoint(8); err != nil {
		t.Fatal(err)
	}
	expectKind(t, s.SetSafePoint(7), ErrRollback)
	if s.SafePoint() != 8 {
		t.Fatal("rejected op must not change state")
	}
}

// TestExpiredValidationFull：过期读写、参数非法与拒绝顺序。
func TestExpiredValidationFull(t *testing.T) {
	s := New(100)
	if err := s.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	expectKind(t, s.Write("k", TypePut, 5, "x"), ErrExpired)
	expectKind(t, s.Write("k", TypePut, 4, "x"), ErrExpired)
	_, _, err := s.Get("k", 4)
	expectKind(t, err, ErrExpired)
	_, err = s.OpenSnapshot(4)
	expectKind(t, err, ErrExpired)

	// 参数非法优先于过期。
	expectKind(t, s.Write("", TypePut, 1, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", 'X', 6, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", TypePut, 0, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", TypePut, maxTS+1, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", TypeDelete, 6, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", TypeLock, 6, "x"), ErrInvalidArgument)
	expectKind(t, s.Write("k", TypeRollback, 6, "x"), ErrInvalidArgument)
	_, _, err = s.Get("", 6)
	expectKind(t, err, ErrInvalidArgument)
	_, err = s.OpenSnapshot(0)
	expectKind(t, err, ErrInvalidArgument)
	expectKind(t, s.SetSafePoint(-1), ErrInvalidArgument)
	_, err = s.GCStep(0)
	expectKind(t, err, ErrInvalidArgument)

	// 过期优先于重复、已满。
	expectKind(t, s.Write("k", TypePut, 5, ""), ErrExpired)
}

// TestDuplicateAndFull：同键同 ts 重复拒绝；记录已满拒绝顺序。
func TestDuplicateAndFull(t *testing.T) {
	s := New(2)
	mustWrite(t, s, "k", TypePut, 10, "a")
	mustWrite(t, s, "k", TypeDelete, 11, "")
	expectKind(t, s.Write("k", TypePut, 10, "b"), ErrDuplicate)
	expectKind(t, s.Write("k", TypeLock, 11, ""), ErrDuplicate)
	expectKind(t, s.Write("k2", TypePut, 12, "c"), ErrFull)
	expectKind(t, s.Write("k", TypePut, 13, "d"), ErrFull)
	// 重复优先于已满。
	expectKind(t, s.Write("k", TypePut, 10, "d"), ErrDuplicate)
	if s.Count() != 2 {
		t.Fatalf("count = %d want 2", s.Count())
	}
}

// TestPutEmptyValue：P 的空串是合法值，可与“不存在”区分。
func TestPutEmptyValue(t *testing.T) {
	s := New(10)
	mustWrite(t, s, "k", TypePut, 3, "")
	v, ok, err := s.Get("k", 3)
	if err != nil || !ok || v != "" {
		t.Fatalf("get empty put = %q,%v,%v", v, ok, err)
	}
	_, ok2, err := s.Get("missing", 3)
	if err != nil || ok2 {
		t.Fatalf("missing key should be not-ok")
	}
}

// TestReadInvariance：GC 前后对所有 ts>=sp 的读结果逐点不变。
func TestReadInvariance(t *testing.T) {
	s := New(1000)
	type w struct {
		k  string
		t  byte
		ts int64
		v  string
	}
	ws := []w{
		{"a", TypePut, 1, "a1"}, {"a", TypeLock, 2, ""}, {"a", TypePut, 4, "a4"},
		{"a", TypeDelete, 7, ""}, {"a", TypePut, 9, "a9"},
		{"b", TypePut, 2, "b2"}, {"b", TypeRollback, 3, ""}, {"b", TypeDelete, 5, ""},
		{"c", TypeLock, 1, ""}, {"c", TypePut, 6, "c6"},
	}
	for _, x := range ws {
		mustWrite(t, s, x.k, x.t, x.ts, x.v)
	}
	readAll := func(minTS int64) map[string]map[int64]string {
		out := map[string]map[int64]string{}
		for _, k := range []string{"a", "b", "c", "missing"} {
			out[k] = map[int64]string{}
			for ts := minTS; ts <= 12; ts++ {
				v, ok, err := s.Get(k, ts)
				if err != nil {
					t.Fatalf("get %s@%d: %v", k, ts, err)
				}
				if ok {
					out[k][ts] = v
				}
			}
		}
		return out
	}
	before := readAll(1)
	for _, sp := range []int64{3, 6, 8} {
		if err := s.SetSafePoint(sp); err != nil {
			t.Fatal(err)
		}
		for {
			n, err := s.GCStep(1)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		after := readAll(sp)
		for k := range before {
			for ts := int64(sp); ts <= 12; ts++ {
				gv, gok := after[k][ts]
				_, bok := before[k][ts]
				if gok != bok || (gok && gv != before[k][ts]) {
					t.Fatalf("invariance broken k=%s ts=%d sp=%d", k, ts, sp)
				}
			}
		}
		before = after
	}
}
