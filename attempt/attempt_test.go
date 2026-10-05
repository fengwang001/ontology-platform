package attempt

import (
	"errors"
	"reflect"
	"testing"
)

func TestLedger(t *testing.T) {
	cases := []struct {
		name      string
		max       int
		reruns    int // 尝试 Commit 的次数
		wantOK    int // 应成功的次数
		wantRound int
	}{
		{"A=1 不允许重跑", 1, 2, 0, 1},
		{"A=2 允许一次重跑", 2, 3, 1, 2},
		{"A=5 允许四次重跑", 5, 6, 4, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New(tc.max)
			if l.Round() != 0 {
				t.Fatalf("Begin 前 Round()=%d, want 0", l.Round())
			}
			l.Begin()
			if l.Round() != 1 {
				t.Fatalf("Begin 后 Round()=%d, want 1", l.Round())
			}
			ok := 0
			for i := 0; i < tc.reruns; i++ {
				err := l.Commit([]int{i, i + 1})
				if err == nil {
					ok++
				} else if !errors.Is(err, ErrExhausted) {
					t.Fatalf("超限应报 ErrExhausted, got %v", err)
				}
			}
			if ok != tc.wantOK {
				t.Fatalf("成功重跑 %d 次, want %d", ok, tc.wantOK)
			}
			if l.Round() != tc.wantRound {
				t.Fatalf("Round()=%d, want %d", l.Round(), tc.wantRound)
			}
			if !l.Exhausted() {
				t.Fatalf("已用轮次 %d 达到上限 %d 应报超限", l.Round(), l.Max())
			}
			if got := len(l.Records()); got != tc.wantOK {
				t.Fatalf("重跑账 %d 条, want %d", got, tc.wantOK)
			}
			t.Logf("输入=max:%d 尝试重跑:%d 输出=成功:%d 轮次:%d 判定依据=已用轮次达到 A 即超限",
				tc.max, tc.reruns, ok, l.Round())
		})
	}
}

func TestLedgerRecordsJobs(t *testing.T) {
	l := New(3)
	l.Begin()
	if err := l.Commit([]int{0, 2, 3}); err != nil {
		t.Fatalf("Commit 报错: %v", err)
	}
	recs := l.Records()
	want := []Record{{Round: 2, Jobs: []int{0, 2, 3}}}
	if !reflect.DeepEqual(recs, want) {
		t.Fatalf("重跑账 = %+v, want %+v", recs, want)
	}
}
