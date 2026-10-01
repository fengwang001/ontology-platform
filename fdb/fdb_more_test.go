package fdb

import (
	"reflect"
	"testing"
)

// TestMulticastSourceNotLearned 组播源不学习但帧照常转发。
func TestMulticastSourceNotLearned(t *testing.T) {
	f := mustNew(t, 3, 100, 10)
	out, err := f.Frame(0, macM, macA, 1, 0)
	if err != nil || !reflect.DeepEqual(out, []int{1, 2}) {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if _, ok, _ := f.Lookup(1, macM, 0); ok || f.Len() != 0 || f.Stats().Learned != 0 {
		t.Fatalf("multicast source must not be learned")
	}
	out, err = f.Frame(1, macBC, macBC, 1, 1)
	if err != nil || !reflect.DeepEqual(out, []int{0, 2}) {
		t.Fatalf("broadcast flood out=%v err=%v", out, err)
	}
	if f.Len() != 0 {
		t.Fatalf("broadcast source must not be learned")
	}
	t.Logf("输入 Frame(p=1,s=FF:..:FF,d=FF:..:FF,t=1) 输出 %v 判定: 组播源不学习, 组播目的泛洪", out)
}

// TestFloodFilterForward 验证泛洪集合、命中转发、同口过滤与源目同主机。
func TestFloodFilterForward(t *testing.T) {
	f := mustNew(t, 4, 100, 10)
	// 源目同一主机：无表项先学习，随后转发命中同端口 -> 过滤。
	out, err := f.Frame(0, macA, macA, 1, 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("same host want filtered empty, got %v %v", out, err)
	}
	if st := f.Stats(); st.Filtered != 1 {
		t.Fatalf("Filtered want 1, got %d", st.Filtered)
	}
	t.Logf("输入 Frame(p=0,s=A,d=A,t=2) 输出 [] 判定: 先学习(刷新)后转发, 同口过滤")

	// 学 B@2（源目同机，被过滤）。
	if _, err := f.Frame(2, macB, macB, 1, 1); err != nil {
		t.Fatal(err)
	}

	out, err = f.Frame(1, macC, macA, 1, 2)
	if err != nil || !reflect.DeepEqual(out, []int{0}) {
		t.Fatalf("known unicast want [0], got %v err=%v", out, err)
	}
	t.Logf("输入 Frame(p=1,s=C,d=A,t=3) 输出 [0] 判定: 学 C@1, 命中 A@0 转发")

	out, err = f.Frame(1, macC, MAC{0, 0, 0, 0, 0, 9}, 1, 3)
	if err != nil || !reflect.DeepEqual(out, []int{0, 2, 3}) {
		t.Fatalf("unknown unicast flood want [0 2 3], got %v err=%v", out, err)
	}
	t.Logf("输入未知单播 d=..:09 输出 %v 判定: 无表项泛洪除入端口外全部端口", out)

	out, err = f.Frame(3, macC, macM, 1, 4)
	if err != nil || !reflect.DeepEqual(out, []int{0, 1, 2}) {
		t.Fatalf("multicast flood want [0 1 2], got %v err=%v", out, err)
	}
	t.Logf("输入组播目的 输出 %v 判定: 组播泛洪", out)

	out, err = f.Frame(2, macB, macA, 1, 5)
	if err != nil || !reflect.DeepEqual(out, []int{0}) {
		t.Fatalf("B->A want [0], got %v err=%v", out, err)
	}
	if st := f.Stats(); st.Floods != 2 {
		t.Fatalf("Floods want 2, got %d", st.Floods)
	}
}

// TestFlushPort 验证 FlushPort 只删动态、删后泛洪、静态保留。
func TestFlushPort(t *testing.T) {
	f := mustNew(t, 3, 1_000_000, 10)
	if _, err := f.Frame(0, macA, macM, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Frame(1, macB, macM, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.AddStatic(1, macC, 0, 2); err != nil {
		t.Fatal(err)
	}

	n, err := f.FlushPort(0, 3)
	if err != nil || n != 1 {
		t.Fatalf("FlushPort(0) want removed 1, got %d %v", n, err)
	}
	if _, ok, _ := f.Lookup(1, macA, 3); ok {
		t.Fatalf("A dynamic on port0 must be flushed")
	}
	if _, ok, _ := f.Lookup(1, macB, 3); !ok {
		t.Fatalf("B on port1 must remain")
	}
	if port, ok, _ := f.Lookup(1, macC, 3); !ok || port != 0 {
		t.Fatalf("static C must remain even though on flushed port")
	}
	t.Logf("输入 FlushPort(p=0,t=3) 输出 removed=1 判定: 仅删动态 A, 静态 C 保留")

	out, err := f.Frame(1, macB, macA, 1, 4)
	if err != nil || !reflect.DeepEqual(out, []int{0, 2}) {
		t.Fatalf("after flush unknown A must flood, got %v err=%v", out, err)
	}
	t.Logf("输入 Frame(p=1,d=A,t=4) 输出 %v 判定: A 已被 flush -> 泛洪", out)

	if st := f.Stats(); st.Flushed != 1 || f.Len() != 1 {
		t.Fatalf("want Flushed=1 Len(dynamic)=1, got %+v Len=%d", st, f.Len())
	}
}
