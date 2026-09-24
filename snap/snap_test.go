package snap

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// genStream 生成 c 通道「随机 records、Bar(1..k)」并随机交错（保每通道顺序），
// 返回交错事件、每 epoch 批量参照快照、全部 records 之和。
func genStream(rng *rand.Rand, c, k int) (merged []Event, ref map[int]int, total int) {
	per := make([][]Event, c)
	ref = map[int]int{}
	for ch := 0; ch < c; ch++ {
		sum := 0
		for id := 1; id <= k; id++ {
			for j, n := 0, rng.Intn(4); j < n; j++ {
				v := rng.Intn(21) - 10
				per[ch] = append(per[ch], Event{Rec, ch, v})
				sum, total = sum+v, total+v
			}
			ref[id] += sum // 逐通道累加「该通道 Bar(id) 之前」之和 = 批量参照
			per[ch] = append(per[ch], Event{Bar, ch, id})
		}
		per[ch] = append(per[ch], Event{Rec, ch, 1})
		total++ // 末世代屏障之后仍有记录
	}
	idx := make([]int, c) // 随机交错：每步从还有事件的通道随机挑一条
	tot := 0
	for _, p := range per {
		tot += len(p)
	}
	for len(merged) < tot {
		var cand []int
		for ch := 0; ch < c; ch++ {
			if idx[ch] < len(per[ch]) {
				cand = append(cand, ch)
			}
		}
		ch := cand[rng.Intn(len(cand))]
		merged = append(merged, per[ch][idx[ch]])
		idx[ch]++
	}
	return
}
func TestBatchReference(t *testing.T) { // 不变量 1：快照逐 id 等于朴素批量结果
	for _, tc := range []struct{ c, k, seed int }{{2, 1, 1}, {3, 2, 2}, {4, 3, 3}, {8, 4, 4}} {
		t.Run(fmt.Sprintf("C%dK%d", tc.c, tc.k), func(t *testing.T) {
			evs, ref, _ := genStream(rand.New(rand.NewSource(int64(tc.seed))), tc.c, tc.k)
			e, _ := NewEngine(tc.c)
			_ = e.Feed(evs)
			for id, want := range ref {
				if got, ok := e.Snapshot(id); !ok || got != want {
					t.Errorf("snap[%d]=%d(ok=%v) want %d", id, got, ok, want)
				}
			}
			if len(e.emitted) != tc.k || e.emitted[tc.k-1] != tc.k {
				t.Errorf("emitted=%v want in-order %d", e.emitted, tc.k)
			}
		})
	}
}
func TestExactlyOnce(t *testing.T) { // 不变量 2：终值 == 全部 records 之和
	for seed := int64(1); seed <= 30; seed++ {
		evs, _, want := genStream(rand.New(rand.NewSource(seed)), 3, 3)
		e, _ := NewEngine(3)
		_ = e.Feed(evs)
		if e.Sum() != want {
			t.Errorf("seed %d: sum=%d want %d", seed, e.Sum(), want)
		}
	}
}
func TestBarrierGatesOwnChannel(t *testing.T) { // 不变量 3：第三节八事件
	e, _ := NewEngine(2)
	evs := []Event{{Rec, 0, 5}, {Rec, 1, 10}, {Bar, 0, 1}, {Rec, 0, 7},
		{Rec, 1, 3}, {Bar, 1, 1}, {Rec, 0, 2}, {Rec, 1, 1}}
	want := []int{5, 15, 15, 15, 18, 25, 27, 28}
	for i, ev := range evs {
		_ = e.Feed([]Event{ev})
		if e.Sum() != want[i] {
			t.Errorf("step %d: sum=%d want %d", i+1, e.Sum(), want[i])
		}
	}
	if v, _ := e.Snapshot(1); v != 18 || len(e.emitted) != 1 || e.emitted[0] != 1 {
		t.Errorf("snap[1]=%d emitted=%v, want 18/[1]", v, e.emitted)
	}
}
func TestSentinelErrorsDistinct(t *testing.T) { // 四类哨兵互不相同
	s := []error{ErrInvalidChannels, ErrChannelOutOfRange, ErrBarrierNonPositive, ErrBarrierOutOfOrder}
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[i] == s[j] {
				t.Errorf("sentinels %d,%d identical", i, j)
			}
		}
	}
}
func TestRejectedBatchAtomic(t *testing.T) { // 不变量 4：拒批不留痕，之后仍可用
	if _, err := NewEngine(0); !errors.Is(err, ErrInvalidChannels) {
		t.Fatalf("New(0): %v", err)
	}
	type tc struct {
		name string
		bad  []Event
		want error
	}
	cases := []tc{
		{"ch negative", []Event{{Rec, -1, 1}}, ErrChannelOutOfRange},
		{"ch too large", []Event{{Bar, 2, 1}}, ErrChannelOutOfRange},
		{"bar zero", []Event{{Bar, 0, 0}}, ErrBarrierNonPositive},
		{"bar negative", []Event{{Bar, 0, -3}}, ErrBarrierNonPositive},
		{"dup channel", []Event{{Bar, 0, 1}, {Bar, 0, 1}}, ErrBarrierOutOfOrder},
		{"regress", []Event{{Bar, 0, 2}, {Bar, 0, 1}}, ErrBarrierOutOfOrder},
		{"good then bad", []Event{{Rec, 0, 999}, {Bar, 1, 0}}, ErrBarrierNonPositive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := NewEngine(2)
			if err := e.Feed(c.bad); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			if e.Sum() != 0 || len(e.snaps) != 0 || e.blocked != 0 {
				t.Fatalf("trace left: sum=%d snaps=%d blocked=%d", e.Sum(), len(e.snaps), e.blocked)
			}
			if err := e.Feed([]Event{{Rec, 0, 4}}); err != nil || e.Sum() != 4 {
				t.Fatalf("unusable after reject: err=%v sum=%d", err, e.Sum())
			}
		})
	}
}
func TestAlignmentCheckConstant(t *testing.T) { // 完成判定检查通道数不随 m 线性增长
	for _, m := range []int{100, 1000, 10000} {
		e, _ := NewEngine(m)
		for ch := 0; ch < m-1; ch++ {
			_ = e.Feed([]Event{{Bar, ch, 1}})
		}
		if e.blocked != m-1 {
			t.Fatalf("m=%d blocked=%d", m, e.blocked)
		}
		_ = e.Feed([]Event{{Bar, m - 1, 1}})
		if e.lastCheckCount != 1 { // 靠 blocked 计数 O(1) 判定，只查刚到的 1 条
			t.Errorf("m=%d lastCheckCount=%d want 1", m, e.lastCheckCount)
		}
		if v, ok := e.Snapshot(1); !ok || v != 0 {
			t.Errorf("m=%d snap=%d ok=%v want 0", m, v, ok)
		}
	}
}
