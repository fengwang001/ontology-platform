package meeting

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"testing"
)

// TestHandQueueOrder 验证 FIFO 次序：入队、队首、任意移出、名次
// 都与朴素切片模型一致。
func TestHandQueueOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	q := newHandQueue(1)
	var model []string
	users := make([]string, 64)
	for i := range users {
		users[i] = fmt.Sprintf("u%02d", i)
	}
	for step := 0; step < 4000; step++ {
		switch rng.Intn(4) {
		case 0, 1: // 入队一个不在队列中的用户
			u := users[rng.Intn(len(users))]
			if q.contains(u) {
				continue
			}
			q.pushBack(u)
			model = append(model, u)
		case 2: // 移出随机一个队列成员
			if len(model) == 0 {
				continue
			}
			i := rng.Intn(len(model))
			q.remove(model[i])
			model = append(model[:i], model[i+1:]...)
		case 3: // 弹出队首
			if len(model) == 0 {
				continue
			}
			got, ok := q.popFront()
			if !ok || got != model[0] {
				t.Fatalf("step %d: popFront = %q,%v; want %q", step, got, ok, model[0])
			}
			model = model[1:]
		}
		if got := q.order(); !reflect.DeepEqual(got, model) {
			t.Fatalf("step %d: order = %v; want %v", step, got, model)
		}
		if q.len() != len(model) {
			t.Fatalf("step %d: len = %d; want %d", step, q.len(), len(model))
		}
		for i, u := range model {
			if p := q.pos(u); p != i+1 {
				t.Fatalf("step %d: pos(%q) = %d; want %d", step, u, p, i+1)
			}
		}
		if p := q.pos("ghost"); p != 0 {
			t.Fatalf("step %d: pos(ghost) = %d; want 0", step, p)
		}
		if front, ok := q.front(); len(model) == 0 && ok || len(model) > 0 && (!ok || front != model[0]) {
			t.Fatalf("step %d: front = %q,%v; want %q", step, front, ok, model)
		}
	}
}

// TestHandQueueComplexityBound 以可验证方式证明操作开销不随队列长度线性增长：
// 满容量（500）时树高有对数级上界，且 pos/remove 的实际结点访问数
// 以树高的常数倍为上界。
func TestHandQueueComplexityBound(t *testing.T) {
	const n = MaxQueueCap
	q := newHandQueue(7)
	for i := 0; i < n; i++ {
		q.pushBack(fmt.Sprintf("u%03d", i))
	}
	height := q.height()
	// 随机 Treap 期望树高约 2.4·ln n；取 4·log2(n+1) 作为宽松上界。
	bound := 4 * (bits.Len(uint(n)) + 1)
	if height > bound {
		t.Fatalf("height = %d exceeds bound %d (n=%d)", height, bound, n)
	}
	t.Logf("n=%d height=%d bound=%d", n, height, bound)

	// 名次查询：访问结点数不超过树高。
	q.resetVisits()
	if p := q.pos("u499"); p != n {
		t.Fatalf("pos(u499) = %d; want %d", p, n)
	}
	if q.visits > height {
		t.Fatalf("pos visited %d nodes > height %d", q.visits, height)
	}
	t.Logf("pos visits=%d (height=%d)", q.visits, height)

	// 任意移出：两次分裂加一次合并，访问数不超过 3 倍树高。
	q.resetVisits()
	q.remove("u250")
	if q.visits > 3*height {
		t.Fatalf("remove visited %d nodes > 3*height %d", q.visits, 3*height)
	}
	t.Logf("remove visits=%d (3*height=%d)", q.visits, 3*height)

	// 弹出队首：定位最左结点加一次分裂。
	q.resetVisits()
	q.popFront()
	if q.visits > 2*height {
		t.Fatalf("popFront visited %d nodes > 2*height %d", q.visits, 2*height)
	}
	t.Logf("popFront visits=%d (2*height=%d)", q.visits, 2*height)
}

// TestHandQueueDeterministicShape 相同种子与操作序列得到相同树形，
// 这是“相同操作序列重放结果完全一致”在数据结构层面的保证。
func TestHandQueueDeterministicShape(t *testing.T) {
	build := func() *handQueue {
		q := newHandQueue(99)
		for i := 0; i < 200; i++ {
			q.pushBack(fmt.Sprintf("u%03d", i))
		}
		for i := 0; i < 200; i += 3 {
			q.remove(fmt.Sprintf("u%03d", i))
		}
		return q
	}
	a, b := build(), build()
	if !reflect.DeepEqual(a.order(), b.order()) || a.height() != b.height() {
		t.Fatal("same seed and ops produced different treap")
	}
}
