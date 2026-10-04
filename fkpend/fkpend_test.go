package fkpend

import "testing"

func k(id int64) Key { return Key{Shard: 1, Kind: 1, ID: id} }

// 非导出计数器：释放时检视的挂起行数等于被释放与被重新挂起的行数之和，
// 与队列总长无关。
func TestReleaseInspectionCounter(t *testing.T) {
	q := New(100, 1_000_000_000)
	// 50 行挂起，只有 1、2、3 等待触发键 999。
	for i := int64(1); i <= 50; i++ {
		wait := k(1000 + i)
		if i <= 3 {
			wait = k(999)
		}
		q.Add(k(i), int64(i), 0, wait, int64(i))
	}
	judge := func(r *Row) (bool, Key) {
		if r.Key.ID == 3 {
			return false, k(2000) // 重新挂起
		}
		return true, Key{} // 释放
	}
	q.Release(k(999), judge)
	if q.inspected != 3 {
		t.Fatalf("inspected=%d, want 3 (2 released + 1 repended)", q.inspected)
	}
	if q.Len() != 48 {
		t.Fatalf("Len=%d, want 48", q.Len())
	}
	r, ok := q.Row(k(3))
	if !ok || r.Wait != k(2000) || r.Aseq != 3 || r.Arrival != 3 {
		t.Fatalf("row 3 = %+v ok=%v, want wait=2000 aseq=3 arrival=3", r, ok)
	}
}

// 释放按 aseq 升序、广度优先：落库行的等待者追加到队尾。
func TestReleaseBFSOrder(t *testing.T) {
	q := New(100, 1_000_000_000)
	// 等待 10：12(aseq1)、13(aseq2)；等待 12：14(aseq3)；等待 13：15(aseq4)。
	q.Add(k(12), 11, 0, k(10), 1)
	q.Add(k(13), 11, 0, k(10), 2)
	q.Add(k(14), 12, 0, k(12), 3)
	q.Add(k(15), 13, 0, k(13), 4)
	var order []int64
	q.Release(k(10), func(r *Row) (bool, Key) {
		order = append(order, r.Key.ID)
		return true, Key{}
	})
	want := []int64{12, 13, 14, 15}
	if len(order) != len(want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v (BFS)", order, want)
		}
	}
	if q.inspected != 4 || q.Len() != 0 {
		t.Fatalf("inspected=%d Len=%d, want 4,0", q.inspected, q.Len())
	}
}

// 到期按 aseq 升序移出；边界 now-arrival == timeout 触发。
func TestExpireOrderAndBoundary(t *testing.T) {
	q := New(10, 10)
	q.Add(k(1), 0, 0, k(9), 5)
	q.Add(k(2), 0, 0, k(9), 7)
	q.Add(k(3), 0, 0, k(9), 6)
	out := q.Expire(14) // 14-5=9,14-7=7,14-6=8 均 < 10
	if len(out) != 0 || q.Len() != 3 {
		t.Fatalf("Expire(14)=%d rows Len=%d, want 0,3", len(out), q.Len())
	}
	out = q.Expire(16) // 1 与 3 到期（16-5=11,16-6=10），按 aseq 升序
	if len(out) != 2 || out[0].Key != k(1) || out[1].Key != k(3) {
		t.Fatalf("Expire(16)=%v, want [1 3]", out)
	}
	if q.Len() != 1 || !q.Has(k(2)) {
		t.Fatalf("Len=%d Has(2)=%v, want 1,true", q.Len(), q.Has(k(2)))
	}
}

// 替换保留 aseq 与到达 now；容量只对新增生效。
func TestReplaceAndCapacity(t *testing.T) {
	q := New(1, 100)
	q.Add(k(1), 5, 0, k(9), 3)
	if !q.Full() {
		t.Fatal("want full")
	}
	q.Replace(k(1), 6, 0, k(8))
	r, _ := q.Row(k(1))
	if r.A != 6 || r.Aseq != 1 || r.Arrival != 3 || r.Wait != k(8) {
		t.Fatalf("row=%+v, want a=6 aseq=1 arrival=3 wait=8", r)
	}
	if !q.Remove(k(1)) || q.Remove(k(1)) {
		t.Fatal("Remove semantics broken")
	}
	if q.Full() || q.Len() != 0 {
		t.Fatal("want empty")
	}
}
