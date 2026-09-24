package inst

import (
	"testing"

	"ontology/rule"
)

// buildLog 发布 n 个版本：奇数位置放/覆盖 r，偶数位置不影响本测试；这里统一 Put r=1。
func buildLog(t *testing.T, n int) *rule.Log {
	t.Helper()
	l := rule.NewLog()
	for i := 0; i < n; i++ {
		if err := l.Append(rule.Put{ID: "r", Threshold: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// TestScanComplexity 钉住第四节：缓冲按 tag 有序、只看队头。
// 早版本(<G)检查条数为与 m 无关的小常数；末版本恰好刷 m 条时检查数 ≤ m+常数。
func TestScanComplexity(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		log := buildLog(t, 10)
		in := New(0, m*2)
		for i := 0; i < m; i++ { // m 条 tag==G=10 的数据全部入缓冲
			if _, err := in.Receive(Data{Key: int64(i), Val: 100}, 10); err != nil {
				t.Fatalf("m=%d receive: %v", m, err)
			}
		}
		if in.Buffered() != m {
			t.Fatalf("m=%d buffered=%d want %d", m, in.Buffered(), m)
		}
		in.Apply(log.At(1)) // 只到 v=1（<G），tag=10 的一条都不能刷
		if in.lastScan > 2 {
			t.Fatalf("m=%d early scan=%d 应与 m 无关", m, in.lastScan)
		}
		if in.Buffered() != m || in.V() != 1 {
			t.Fatalf("m=%d early flush 误动缓冲: buf=%d v=%d", m, in.Buffered(), in.V())
		}
		for v := 2; v <= 9; v++ { // 追到 v=9，每次仍只看队头
			in.Apply(log.At(v))
			if in.lastScan > 2 {
				t.Fatalf("m=%d v=%d scan=%d", m, v, in.lastScan)
			}
		}
		in.Apply(log.At(10)) // 末版本：恰好刷出 m 条
		if in.lastScan > m+1 {
			t.Fatalf("m=%d final scan=%d 应 ≤ 刷出条数+常数(%d)", m, in.lastScan, m+1)
		}
		if in.Buffered() != 0 {
			t.Fatalf("m=%d final buf=%d want 0", m, in.Buffered())
		}
	}
}

func TestInstanceInvariant(t *testing.T) {
	cases := []struct {
		name string
		G    int
		appl int   // 预先应用的版本数
		tags []int // 随后以这些 tag Receive
		want bool
	}{
		{"空", 0, 0, nil, true},
		{"缓冲 tag 均大于 v=0", 3, 0, []int{1, 2, 3}, true},
		{"应用两版后缓冲仍领先", 5, 2, []int{3, 4, 5}, true},
		{"应用后只剩未来 tag", 5, 5, []int{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := New(0, 1000)
			log := rule.NewLog()
			for k := 0; k < c.appl; k++ {
				_ = log.Append(rule.Put{ID: "r", Threshold: 1})
				in.Apply(log.At(k + 1))
			}
			for _, tg := range c.tags {
				if _, err := in.Receive(Data{Key: 1, Val: 1}, tg); err != nil {
					t.Fatal(err)
				}
			}
			if got := in.Invariant(c.G); got != c.want {
				t.Fatalf("%s invariant=%v want %v (v=%d)", c.name, got, c.want, in.V())
			}
		})
	}
}

func TestBufferFullLeavesNoTrace(t *testing.T) {
	in := New(7, 1)
	if _, err := in.Receive(Data{Key: 0, Val: 0}, 5); err != nil { // v=0<5 入缓冲
		t.Fatal(err)
	}
	before := in.Buffered()
	if _, err := in.Receive(Data{Key: 1, Val: 0}, 5); err != ErrBufferFull || in.Buffered() != before {
		t.Fatalf("want ErrBufferFull 且不留痕, buf=%d", in.Buffered())
	}
	if _, err := in.Receive(Data{Key: 2, Val: 0}, 0); err != nil { // 拒绝后仍可继续（立即处理）
		t.Fatalf("拒绝后应可继续使用: %v", err)
	}
}

func TestPerInstanceBufferLimit(t *testing.T) {
	// 上限按实例各自计：两个 maxBuf=1 的实例应能各持一条，而非全部实例合计 ≤1。
	x, y := New(0, 1), New(1, 1)
	if _, err := x.Receive(Data{Key: 0, Val: 0}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := y.Receive(Data{Key: 1, Val: 0}, 3); err != nil {
		t.Fatalf("另一实例应仍能各持一条: %v", err)
	}
	if _, err := x.Receive(Data{Key: 2, Val: 0}, 3); err != ErrBufferFull {
		t.Fatalf("同一实例第二条应被拒, got %v", err)
	}
}

func TestFlushUsesSnapshotRules(t *testing.T) {
	log := rule.NewLog()
	_ = log.Append(rule.Put{ID: "r1", Threshold: 10}) // v1 有 r1
	in := New(3, 10)
	if _, err := in.Receive(Data{Key: 0, Val: 12}, 1); err != nil { // tag=1 但 v=0，先缓冲
		t.Fatal(err)
	}
	_ = log.Append(rule.Delete{ID: "r1"}) // v2 删除 r1
	hits := in.Apply(log.At(1))           // 刷 tag=1：必须用 v1 快照
	if len(hits) != 1 || hits[0].RuleID != "r1" || hits[0].Ver != 1 || hits[0].Inst != 3 {
		t.Fatalf("刷出必须按 tag 快照, got %+v", hits)
	}
}
