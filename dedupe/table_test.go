package dedupe

import "testing"

func TestFIFOEvictionAndNoRefresh(t *testing.T) {
	tab := New(3)
	if tab == nil || New(0) != nil || New(1_000_001) != nil {
		t.Fatalf("capacity bounds wrong")
	}
	put := func(k string, v int) { tab.Put([]byte(k), v) }
	put("a", 1)
	put("b", 2)
	put("c", 3)
	if v, ok := tab.Lookup([]byte("a")); !ok || v.(int) != 1 {
		t.Fatalf("lookup a: %v %v", v, ok)
	}
	// 命中不刷新：插入 d 淘汰的仍是最旧的 a，尽管刚查过 a。
	put("d", 4)
	if _, ok := tab.Lookup([]byte("a")); ok {
		t.Fatalf("a must be evicted (lookup does not refresh)")
	}
	if tab.Len() != 3 {
		t.Fatalf("len=%d", tab.Len())
	}
	t.Logf("IN lookup(a) 后 put(d) -> OUT 仍淘汰 a（命中不刷新），大小=3=K")
}

func TestPutExistingMovesToBack(t *testing.T) {
	tab := New(2)
	tab.Put([]byte("a"), 1)
	tab.Put([]byte("b"), 2)
	tab.Put([]byte("a"), 10) // 更新值并移到最新端
	if v, _ := tab.Lookup([]byte("a")); v.(int) != 10 {
		t.Fatalf("a value=%v", v)
	}
	tab.Put([]byte("c"), 3) // 淘汰的应是 b，不是 a
	if _, ok := tab.Lookup([]byte("b")); ok {
		t.Fatalf("b must be evicted after a moved to back")
	}
	if v, _ := tab.Lookup([]byte("a")); v.(int) != 10 {
		t.Fatalf("a should survive: %v", v)
	}
	t.Logf("IN Put(a) 更新 -> 移到最新端；Put(c) 时淘汰 b，a 保留")
}
