package fdb

import (
	"errors"
	"reflect"
	"testing"
)

func macOf(b ...byte) MAC {
	var m MAC
	copy(m[:], b)
	return m
}

var (
	macA  = macOf(0x00, 0x00, 0x00, 0x00, 0x00, 0x01)
	macB  = macOf(0x00, 0x00, 0x00, 0x00, 0x00, 0x02)
	macC  = macOf(0x00, 0x00, 0x00, 0x00, 0x00, 0x03)
	macM  = macOf(0x01, 0x00, 0x00, 0x00, 0x00, 0x01) // 首字节最低位 1：组播
	macBC = MAC{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
)

func mustNew(t *testing.T, n int, a int64, c int) *FDB {
	t.Helper()
	f, err := New(n, a, c)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", n, a, c, err)
	}
	return f
}

// TestAgingBoundary 验证 t-seen 恰为 A-1 仍命中、恰为 A 已老化。
func TestAgingBoundary(t *testing.T) {
	const a int64 = 10
	f := mustNew(t, 3, a, 10)

	out, err := f.Frame(0, macA, macB, 1, 0)
	if err != nil || !reflect.DeepEqual(out, []int{1, 2}) {
		t.Fatalf("learn frame out=%v err=%v", out, err)
	}
	if port, ok, _ := f.Lookup(1, macA, 9); !ok || port != 0 {
		t.Fatalf("Lookup t=9 want port0 hit, got %d,%v", port, ok)
	}
	out, err = f.Frame(2, macB, macA, 1, 9)
	if err != nil || !reflect.DeepEqual(out, []int{0}) {
		t.Fatalf("A-1 want forward [0], got %v err=%v", out, err)
	}
	t.Logf("输入 Frame(p=2,s=B,d=A,v=1,t=9) 输出 %v 判定: age=9=A-1 未过期单播", out)

	out, err = f.Frame(2, macC, macA, 1, 10)
	if err != nil || !reflect.DeepEqual(out, []int{0, 1}) {
		t.Fatalf("A want flood [0 1], got %v err=%v", out, err)
	}
	t.Logf("输入 Frame(p=2,s=C,d=A,v=1,t=10) 输出 %v 判定: age=10=A 左闭过期->清除->未知泛洪", out)
	if st := f.Stats(); st.Expired != 1 || st.Learned != 3 {
		t.Fatalf("stats after aging = %+v", st)
	}

	if _, ok, _ := f.Lookup(1, macA, 100); ok {
		t.Fatalf("Lookup expired entry must miss")
	}
	if st := f.Stats(); st.Expired != 1 {
		t.Fatalf("Lookup must not change counts, Expired=%d", st.Expired)
	}
}

// TestRefreshAndMove 验证同端口刷新 seen 不记 Moves，换端口迁移记一次。
func TestRefreshAndMove(t *testing.T) {
	f := mustNew(t, 4, 100, 10)
	if _, err := f.Frame(1, macA, macM, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Frame(1, macA, macM, 1, 50); err != nil {
		t.Fatal(err)
	}
	if st := f.Stats(); st.Moves != 0 || st.Learned != 1 {
		t.Fatalf("same-port refresh must not move/learn: %+v", st)
	}
	seen50, _, _ := f.inspect(1, macA)
	if seen50 != 50 {
		t.Fatalf("seen refreshed want 50, got %d", seen50)
	}
	t.Logf("输入 Frame(p=1,s=A,...,t=50) 判定: 同端口仅刷新 seen 0->50, Moves=0")

	if _, err := f.Frame(3, macA, macM, 1, 60); err != nil {
		t.Fatal(err)
	}
	st := f.Stats()
	if st.Moves != 1 || st.Learned != 1 {
		t.Fatalf("migration stats = %+v", st)
	}
	if port, ok, _ := f.Lookup(1, macA, 60); !ok || port != 3 {
		t.Fatalf("after move want port 3")
	}
	t.Logf("输入 Frame(p=3,s=A,...,t=60) 判定: 端口迁移 1->3, Moves=1")
}

// TestStaticProtection 验证静态保护、他口丢弃、覆盖计数与永不过期。
func TestStaticProtection(t *testing.T) {
	f := mustNew(t, 4, 10, 10)
	if _, err := f.Frame(1, macA, macM, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.AddStatic(2, macA, 2, 1); err != nil {
		t.Fatal(err)
	}
	st := f.Stats()
	if st.Overridden != 1 || f.Len() != 0 {
		t.Fatalf("override want Overridden=1 Len=0, got %+v Len=%d", st, f.Len())
	}
	t.Logf("输入 AddStatic(v=2,A,p=2,t=1) 判定: 覆盖动态 Overridden=1, 容量释放")

	if port, ok, _ := f.Lookup(2, macA, 1_000_000); !ok || port != 2 {
		t.Fatalf("static must never age")
	}
	out, err := f.Frame(2, macA, macB, 2, 1_000_001)
	if err != nil || !reflect.DeepEqual(out, []int{0, 1, 3}) {
		t.Fatalf("from static port want flood, got %v %v", out, err)
	}
	out, err = f.Frame(0, macA, macB, 2, 1_000_002)
	if err != nil || len(out) != 0 {
		t.Fatalf("security drop want empty list, got %v %v", out, err)
	}
	if st := f.Stats(); st.SecurityDrops != 1 || st.Floods != 2 {
		t.Fatalf("drop must skip forwarding: %+v", st)
	}
	t.Logf("输入 Frame(p=0,s=A,d=B,t=1000002) 输出 [] 判定: 源为静态且端口不符->SecurityDrops, 不转发")

	if err := f.AddStatic(2, macA, 3, 1_000_003); err != nil {
		t.Fatal(err)
	}
	if port, ok, _ := f.Lookup(2, macA, 1_000_003); !ok || port != 3 {
		t.Fatalf("static re-point want port 3")
	}
	if st := f.Stats(); st.Overridden != 1 {
		t.Fatalf("static over static must not count override: %+v", st)
	}
	if err := f.AddStatic(2, macM, 0, 1_000_004); !errors.Is(err, ErrMulticastStatic) {
		t.Fatalf("multicast static want ErrMulticastStatic, got %v", err)
	}
}

// TestEviction 验证容量满时淘汰 seen 最早者、并列取键字节序最小者。
func TestEviction(t *testing.T) {
	f := mustNew(t, 4, 1_000_000, 2)
	if _, err := f.Frame(0, macB, macM, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Frame(0, macC, macM, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Frame(0, macA, macM, 1, 20); err != nil {
		t.Fatal(err)
	}
	st := f.Stats()
	if st.Evictions != 1 || st.Learned != 3 || f.Len() != 2 {
		t.Fatalf("evict stats=%+v Len=%d", st, f.Len())
	}
	if _, ok, _ := f.Lookup(1, macB, 20); ok {
		t.Fatalf("B should be evicted (tie, smaller key)")
	}
	if _, ok, _ := f.Lookup(1, macC, 20); !ok {
		t.Fatalf("C should survive tie")
	}
	if _, ok, _ := f.Lookup(1, macA, 20); !ok {
		t.Fatalf("A should be present")
	}
	t.Logf("输入 同刻学 B,C 后学 A 判定: seen 并列, 键 (v,mac) 最小者 B 被淘汰")

	if _, err := f.Frame(1, macC, macM, 1, 30); err != nil { // 刷新 C
		t.Fatal(err)
	}
	if _, err := f.Frame(0, macB, macM, 1, 40); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.Lookup(1, macA, 40); ok {
		t.Fatalf("A oldest should be evicted")
	}
	if st := f.Stats(); st.Evictions != 2 {
		t.Fatalf("want 2 evictions, got %d", st.Evictions)
	}
	t.Logf("输入 刷新 C 至 t=30 后再学 B 判定: 最老者 A(seen=20) 被淘汰")

	g := mustNew(t, 2, 1_000_000, 1)
	if err := g.AddStatic(1, macA, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Frame(0, macB, macM, 1, 1); err != nil {
		t.Fatal(err)
	}
	if st := g.Stats(); st.Evictions != 0 || g.Len() != 1 {
		t.Fatalf("static must not consume capacity: %+v", st)
	}
}
