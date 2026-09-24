package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

// genOps 生成确定性随机操作序列（版本变更恒在 vwm 之后，全部应被接受），末尾 Flush。
func genOps(seed int64, n int) []op {
	r := rand.New(rand.NewSource(seed))
	vwm, seq := int64(0), []op(nil)
	for i := 0; i < n; i++ {
		key := string(rune('a' + r.Intn(4)))
		switch r.Intn(3) {
		case 0:
			vwm += 1 + r.Int63n(10)
			seq = append(seq, op{3, "", "", vwm, nil})
		case 1:
			seq = append(seq, op{2, key, "", r.Int63n(400), nil})
		default:
			seq = append(seq, op{r.Intn(2), key, fmt.Sprint(i), vwm + 1 + r.Int63n(200), nil})
		}
	}
	return append(seq, op{4, "", "", 0, nil})
}
func TestNaiveConsistencyAfterFlush(t *testing.T) { // 不变量1：Flush 后与朴素参照一致
	for _, seed := range []int64{1, 2, 3, 5, 8, 13} {
		if err := run(genOps(seed, 300), 400); err != nil {
			t.Errorf("seed %d: %v", seed, err)
		}
	}
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestExactlyOnceAndOrdering(t *testing.T) { // 不变量2：恰好一次、TS<=vwm、批内 (TS,Seq) 升序
	e := New(10)
	for _, ts := range []int64{30, 10, 20, 25, 15} { // 乱序到达，全部进缓冲
		if out, err := e.Feed("k", ts); err != nil || len(out) != 0 {
			t.Fatalf("feed %d: %v %v", ts, out, err)
		}
	}
	batch, _ := e.Watermark(25)
	if len(batch) != 4 {
		t.Fatalf("release: %v", batch)
	}
	for i, o := range batch {
		if o.TS != []int64{10, 15, 20, 25}[i] || o.Seq != []int{1, 4, 2, 3}[i] || o.TS > 25 {
			t.Fatalf("batch[%d]=%+v", i, o)
		}
	}
	e.Flush()
	outs := e.Outputs()
	slices.SortFunc(outs, func(a, b Joined) int { return a.Seq - b.Seq })
	for i, o := range outs {
		if o.Seq != i {
			t.Fatalf("seq %d 缺失或重复", i)
		}
	}
	if len(outs) != 5 {
		t.Fatalf("outputs %d", len(outs))
	}
}
func TestEmittedImmutable(t *testing.T) { // 不变量3：已输出结果不可变
	e := New(4)
	e.Upsert("k", 10, "A")
	e.Watermark(10)
	e.Feed("k", 5)
	snap := e.Outputs()
	e.Upsert("k", 11, "B") // 被接受的变更必 ValidFrom > vwm
	e.Watermark(100)
	e.Flush()
	if now := e.Outputs(); len(now) < len(snap) || !slices.Equal(now[:len(snap)], snap) {
		t.Fatal("已输出结果被改变")
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) { // 不变量4：四类错误互不相同、被拒后状态不变
	for i, a := range []error{ErrEmptyKey, ErrLate, ErrWmBack, ErrBufFull} {
		for j, b := range []error{ErrEmptyKey, ErrLate, ErrWmBack, ErrBufFull} {
			if i != j && errors.Is(a, b) {
				t.Fatalf("错误 %v 与 %v 不可区分", a, b)
			}
		}
	}
	cases := []struct {
		name string
		bad  func(*Engine) error
		want error
	}{
		{"空Key版本", func(e *Engine) error { return e.Upsert("", 100, "x") }, ErrEmptyKey},
		{"空Key事件", func(e *Engine) error { _, err := e.Feed("", 100); return err }, ErrEmptyKey},
		{"迟到版本", func(e *Engine) error { return e.Upsert("k", 50, "x") }, ErrLate},
		{"迟到墓碑", func(e *Engine) error { return e.Delete("k", 50) }, ErrLate},
		{"水位回退", func(e *Engine) error { _, err := e.Watermark(49); return err }, ErrWmBack},
		{"缓冲超限", func(e *Engine) error { _, err := e.Feed("k", 200); return err }, ErrBufFull},
	}
	for _, c := range cases {
		e := New(1)
		e.Watermark(50)
		e.Feed("k", 100) // 占满容量为 1 的缓冲
		before := e.Outputs()
		if err := c.bad(e); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		if !slices.Equal(before, e.Outputs()) {
			t.Fatalf("%s: 被拒后状态改变", c.name)
		}
		e.Watermark(200)             // 释放 seq0
		out, err := e.Feed("k", 150) // 被拒事件不占号：应为 seq1
		if err != nil || len(out) != 1 || out[0].Seq != 1 {
			t.Fatalf("%s: 被拒后不可用或序号被占: %v %v", c.name, out, err)
		}
	}
}
func TestConcurrentFeed(t *testing.T) { // 并发 Feed + Flush：恰好一次且与朴素 AS OF 一致
	const N, M = 16, 50
	e := New(N * M)
	for i := 0; i < 20; i++ {
		e.Upsert("k", int64(i*10), fmt.Sprintf("v%d", i))
	}
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < M; i++ {
				if _, err := e.Feed("k", int64((g*M+i)%200)); err != nil {
					t.Error(err)
				}
			}
		}(g)
	}
	wg.Wait()
	e.Flush()
	outs := e.Outputs()
	slices.SortFunc(outs, func(a, b Joined) int { return a.Seq - b.Seq })
	ok := len(outs) == N*M
	for i, o := range outs {
		if o.Seq != i || !o.Found || o.Value != fmt.Sprintf("v%d", o.TS/10) {
			ok = false
		}
	}
	if !ok {
		t.Fatalf("outputs %d != %d 或序号缺重或值不符", len(outs), N*M)
	}
}
