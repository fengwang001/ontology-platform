package freelist

import (
	"math/rand"
	"testing"

	"ontology/slot"
)

func fill(l *List, slots []slot.Slot) {
	for i := range slots {
		l.Put(slots, int32(i))
	}
}

func TestTakePutFIFO(t *testing.T) {
	slots := make([]slot.Slot, 3)
	l := New()
	fill(l, slots)
	for want := int32(0); want < 3; want++ {
		if got, ok := l.Take(slots); !ok || got != want {
			t.Fatalf("Take = %d,%v；期望 %d,true", got, ok, want)
		}
	}
	if _, ok := l.Take(slots); ok {
		t.Fatal("空链表不应取到槽位")
	}
	l.Put(slots, 2)
	l.Put(slots, 0)
	for _, want := range []int32{2, 0} { // 尾插：先还入的先取出
		if got, ok := l.Take(slots); !ok || got != want {
			t.Fatalf("Take = %d,%v；期望 %d,true", got, ok, want)
		}
	}
	if l.Len() != 0 {
		t.Fatalf("Len = %d；期望 0", l.Len())
	}
}

// 取一个空闲槽位访问的槽位记录数必须是 O(1)：不随容量增长。
func TestTakeVisitedConstant(t *testing.T) {
	for _, capacity := range []int{1000, 100000} {
		slots := make([]slot.Slot, capacity)
		l := New()
		fill(l, slots)
		taken := make([]int32, 0, capacity)
		for {
			i, ok := l.Take(slots)
			if !ok {
				break
			}
			taken = append(taken, i)
		}
		rand.New(rand.NewSource(1)).Shuffle(len(taken), func(a, b int) {
			taken[a], taken[b] = taken[b], taken[a]
		})
		for _, i := range taken[:capacity/2] { // 随机释放一半
			l.Put(slots, i)
		}
		if _, ok := l.Take(slots); !ok {
			t.Fatalf("容量%d：释放一半后应能取到槽位", capacity)
		}
		if l.visited > 4 {
			t.Fatalf("容量%d：一次分配访问了 %d 条槽位记录，超过上限 4", capacity, l.visited)
		}
		t.Logf("容量%d：访问槽位记录数 = %d", capacity, l.visited)
	}
}
