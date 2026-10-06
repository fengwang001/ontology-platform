package scrub

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentWritesAndPatrolsSerializability：
// 高并发写入与巡检下，所有结束态副本必须恰好是某次“完整写入”
// 或某次“完整修复”的形状：版本与两个摘要三者一致/配对，
// 绝不会出现仲裁看到一半的混合状态（版本来自新写、摘要来自旧写）。
// 竞态检测器同时验证无数据竞争。
func TestConcurrentWritesAndPatrolsSerializability(t *testing.T) {
	s := NewStore()
	// 5 节点 5 副本，间隔 0（允许同刻连续巡检，时钟仍单调）。
	reps := []Replica{
		rep(1, 1, "v1"), rep(2, 1, "v1"), rep(3, 1, "v1"),
		rep(4, 1, "v1"), rep(5, 1, "v1"),
	}
	if err := s.CreateBlock(0, 42, reps, 0); err != nil {
		t.Fatal(err)
	}

	var clock int64
	nextTime := func() Time { return Time(atomic.AddInt64(&clock, 1)) }

	const writers, patrolers, rounds = 8, 8, 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				v := int64(2 + i)
				d := digestFor(v)
				// 每个写者写 1..5 个随机固定节点；可能与修复并发。
				nodes := []int{1 + (id+i)%5}
				_ = s.Write(nextTime(), 42, v, d, nodes)
			}
		}(w)
	}
	for p := 0; p < patrolers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				_, _ = s.Patrol(nextTime(), 42, nil)
			}
		}()
	}
	wg.Wait()

	rs, ok := s.replicasOf(42)
	if !ok {
		t.Fatal("block vanished")
	}
	// 不变量：任何副本的 (Version, SavedDigest) 必须是某次真实写入，
	// 且修复成功的副本 ActualDigest == SavedDigest。由于所有操作持同一
	// 临界区，不存在跨操作撕裂；自洽副本还必须满足摘要/版本配套。
	for _, r := range rs {
		if r.SavedDigest == digestFor(r.Version) {
			// 来自某次完整写入或修复。
			continue
		}
		// 位腐注入在本测试中不存在；唯一允许的“不自洽”只能来自
		// 尚未被巡检覆盖的外部状态——这里没有 Rot，因此所有副本
		// 最终未必全部自洽（写可不达法定数且尚未巡检），但
		// SavedDigest 必须严格匹配其版本，证明无撕裂。
		t.Fatalf("torn replica detected: %+v", r)
	}
}

func digestFor(v int64) string {
	switch v {
	case 1:
		return "v1"
	}
	// 与 TestConcurrentWritesAndPatrolsSerializability 写入摘要规则保持一致。
	return "v" + itoaInt(v)
}

func itoaInt(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for v > 0 {
		p--
		buf[p] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[p:])
}

// TestConcurrentClockMonotonic：并发下时钟回退拒绝必须与某个
// 串行顺序一致——所有成功操作时刻构成非递减序列。
func TestConcurrentClockMonotonic(t *testing.T) {
	s := NewStore()
	reps := []Replica{rep(1, 1, "d"), rep(2, 1, "d")}
	if err := s.CreateBlock(0, 1, reps, 0); err != nil {
		t.Fatal(err)
	}

	var maxAccepted int64 = 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 每个 goroutine 只使用离散时刻 g, g+16, ... 之外的乱序值，
			// 部分必然早于已接受时刻，应被拒绝。
			for i := 0; i < 300; i++ {
				at := Time(((g*7 + i*3) % 500))
				err := s.Write(at, 1, int64(1+i%3), "x", nil)
				if err == nil {
					mu.Lock()
					if int64(at) < maxAccepted {
						t.Errorf("serial order violated: %d < %d", at, maxAccepted)
					}
					if int64(at) > maxAccepted {
						maxAccepted = int64(at)
					}
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
}
