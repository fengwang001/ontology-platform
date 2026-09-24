package rbuf

import "testing"

// TestReleaseProbeBound 证明 Release 按 (TS,Seq) 有序取最小而非整表扫描。
// 场景：缓冲先蓄 m 个事件 TS=base+i（初始不释放），再喂一个触发事件 n 把
// 水位线推进到 base+k-1（n 的 TS = 新wm+delay，自身滞留），于是恰好释放
// 最小的 k 个；弹出 k 个后只需再看一个堆顶即止，检查数 <= k+1，与 m 无关。
// m 取多档、k 取 0 和小常数，表驱动循环生成。
func TestReleaseProbeBound(t *testing.T) {
	const delay, base = int64(10), int64(1000)
	ms := []int{100, 317, 1000, 3162, 10000}
	ks := []int{0, 3}
	for _, m := range ms {
		for _, k := range ks {
			var b Buffer
			for i := 0; i < m; i++ { // m 个高 TS 事件蓄积，全程未释放
				b.Buffer(Event{ID: "h", TS: base + int64(i), Seq: int64(i)})
			}
			newWM := base + int64(k) - 1                          // 只淹没 TS<=base+k-1 的 k 个
			n := Event{ID: "n", TS: newWM + delay, Seq: int64(m)} // 触发事件，TS>newWM 滞留
			b.Buffer(n)
			got := b.Release(newWM)
			if len(got) != k {
				t.Fatalf("m=%d k=%d: 释放数=%d，期望 %d", m, k, len(got), k)
			}
			if b.lastProbes > k+1 {
				t.Fatalf("m=%d k=%d: 检查 %d 个事件，超过 k+1=%d（疑似整表扫描）",
					m, k, b.lastProbes, k+1)
			}
			if b.Len() != m-k+1 { // m-k 个未释放旧事件 + 滞留的触发事件 n
				t.Fatalf("m=%d k=%d: 剩余缓冲=%d，期望 %d", m, k, b.Len(), m-k+1)
			}
		}
	}
}
