package experiment

import "testing"

func mustNew(t *testing.T, B, H int, Cd, P int64) *Orchestrator {
	t.Helper()
	o, err := New(B, H, Cd, P)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d) 失败: %v", B, H, Cd, P, err)
	}
	return o
}

func mustClaim(t *testing.T, o *Orchestrator, id string, n int, now int64, want int) {
	t.Helper()
	got, err := o.Claim(id, n, now)
	if err != nil {
		t.Fatalf("Claim(%s,%d,%d) 意外失败: %v", id, n, now, err)
	}
	if got != want {
		t.Fatalf("Claim(%s,%d,%d) 起点=%d, 期望 %d", id, n, now, got, want)
	}
}

func mustErr(t *testing.T, err error, want Reason, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s 应被拒绝(%s), 实际成功", op, want)
	}
	got, ok := ReasonOf(err)
	if !ok {
		t.Fatalf("%s 错误类型不符: %v", op, err)
	}
	if got != want {
		t.Fatalf("%s 拒绝原因=%s, 期望 %s", op, got, want)
	}
}

func checkBucket(t *testing.T, o *Orchestrator, i int, st bucketState, owner string, r int64, k int) {
	t.Helper()
	b := o.buckets[i]
	if b.state != st || b.owner != owner || b.r != r || b.k != k {
		t.Fatalf("桶 %d = {state:%d owner:%q r:%d k:%d}, 期望 {state:%d owner:%q r:%d k:%d}",
			i, b.state, b.owner, b.r, b.k, st, owner, r, k)
	}
}

type snapshot struct {
	buckets []bucket
	exps    map[string]allocation
	g       int64
	maxNow  int64
}

func takeSnapshot(o *Orchestrator) snapshot {
	s := snapshot{
		buckets: make([]bucket, len(o.buckets)),
		exps:    make(map[string]allocation, len(o.exps)),
		g:       o.g,
		maxNow:  o.maxNow,
	}
	copy(s.buckets, o.buckets)
	for k, v := range o.exps {
		s.exps[k] = v
	}
	return s
}

func checkSnapshot(t *testing.T, o *Orchestrator, s snapshot, what string) {
	t.Helper()
	if o.g != s.g || o.maxNow != s.maxNow {
		t.Fatalf("%s 后 g/maxNow 改变: (%d,%d) -> (%d,%d)", what, s.g, s.maxNow, o.g, o.maxNow)
	}
	for i := range o.buckets {
		if o.buckets[i] != s.buckets[i] {
			t.Fatalf("%s 后桶 %d 改变: %+v -> %+v", what, i, s.buckets[i], o.buckets[i])
		}
	}
	if len(o.exps) != len(s.exps) {
		t.Fatalf("%s 后实验数改变: %d -> %d", what, len(s.exps), len(o.exps))
	}
	for k, v := range s.exps {
		if o.exps[k] != v {
			t.Fatalf("%s 后实验 %s 改变: %+v -> %+v", what, k, v, o.exps[k])
		}
	}
}

// TestSpecExample 复现需求中的完整示例。
func TestSpecExample(t *testing.T) {
	o := mustNew(t, 10, 2, 5, 1)

	// 1. Claim(a,3,0) 得 [2,5)。
	mustClaim(t, o, "a", 3, 0, 2)
	// 2. Claim(b,2,0) 得 [5,7)。
	mustClaim(t, o, "b", 2, 0, 5)

	// 3. Resize(a,1,10): a 保留 [2,3), 桶 3、4 冷却 (o=a, r=10, k=1)。
	if err := o.Resize("a", 1, 10); err != nil {
		t.Fatalf("Resize(a,1,10) 失败: %v", err)
	}
	checkBucket(t, o, 2, stateOccupied, "a", 0, 0)
	checkBucket(t, o, 3, stateCooling, "a", 10, 1)
	checkBucket(t, o, 4, stateCooling, "a", 10, 1)

	// 4. Claim(c,2,12): 3、4 要到 15 才对 c 可用, 取起点 7 得 [7,9)。
	mustClaim(t, o, "c", 2, 12, 7)

	// 5. Resize(a,3,13): 向右收回自己的冷却桶 3、4, 得 [2,5)。
	if err := o.Resize("a", 3, 13); err != nil {
		t.Fatalf("Resize(a,3,13) 失败: %v", err)
	}
	checkBucket(t, o, 3, stateOccupied, "a", 10, 1)
	checkBucket(t, o, 4, stateOccupied, "a", 10, 1)

	// 6. Release(b,14): 5、6 冷却至 19 才對他人可用。
	if err := o.Release("b", 14); err != nil {
		t.Fatalf("Release(b,14) 失败: %v", err)
	}
	checkBucket(t, o, 5, stateCooling, "b", 14, 1)
	checkBucket(t, o, 6, stateCooling, "b", 14, 1)

	// 7. 0 时可用桶只有 {9}, 容量不足。
	if _, err := o.Claim("d", 2, 18); err == nil {
		t.Fatalf("Claim(d,2,18) 应失败")
	}
	if _, err := o.Claim("d", 2, 19); err != nil {
		t.Fatalf("Claim(d,2,19) 应成功, 实际: %v", err)
	}
}
