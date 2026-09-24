package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func sp(s string) *string             { return &s }
func ev(g *string, d int64) api.Event { return api.Event{Group: g, Delta: d} }
func mustFeed(t *testing.T, v *api.View, evs ...api.Event) {
	t.Helper()
	if err := v.Feed(evs); err != nil {
		t.Fatal(err)
	}
}

// snapshotOf 返回视图的规范字符串（解引用指针），可安全做相等比较。
func snapshotOf(v *api.View) string {
	s := ""
	for _, gs := range v.Groups() {
		name := "∅"
		if gs.Group != nil {
			name = *gs.Group
		}
		s += fmt.Sprintf("%s=%d;", name, gs.Count)
	}
	return s
}

// 不变量 1：任意事件序列喂完后，视图与朴素批量重算（只累加被接受事件、归零即删）逐组相同。
func TestBatchEquivalence(t *testing.T) {
	// nkey 是朴素参考的键编码：NULL→"\x00"、其余→"\x01"+s，天然区分 nil 与 ""。
	nkey := func(g *string) string {
		if g == nil {
			return "\x00"
		}
		return "\x01" + *g
	}
	keys := []*string{nil, sp(""), sp("a"), sp("b"), sp("c")}
	for trial := 0; trial < 50; trial++ {
		rng, ref := rand.New(rand.NewSource(int64(trial))), map[string]int64{}
		v, _ := api.New(16)
		for i := 0; i < 200; i++ {
			e := ev(keys[rng.Intn(len(keys))], int64(rng.Intn(7)-3))
			k, want := nkey(e.Group), ref[nkey(e.Group)]+e.Delta
			err := v.Feed([]api.Event{e})
			if (err == nil) != (want >= 0) || (err != nil && !errors.Is(err, api.ErrNegativeCount)) {
				t.Fatalf("trial %d 第 %d 步: 视图与参考不一致", trial, i)
			}
			if want > 0 {
				ref[k] = want
			} else if want == 0 {
				delete(ref, k)
			}
		}
		if len(v.Groups()) != len(ref) {
			t.Fatalf("trial %d: 存在组数与参考不一致", trial)
		}
		for _, gs := range v.Groups() {
			if ref[nkey(gs.Group)] != gs.Count {
				t.Fatalf("trial %d: 组计数与参考不一致", trial)
			}
		}
	}
}

// 不变量 2：NULL 组与空串组互不影响。
func TestNullVsEmpty(t *testing.T) {
	v, _ := api.New(16)
	mustFeed(t, v, ev(nil, 2), ev(sp(""), 1), ev(nil, 1), ev(nil, -3))
	if v.Count(nil) != 0 || v.Count(sp("")) != 1 || len(v.Groups()) != 1 {
		t.Fatal("NULL 与空串混组或归零未删")
	}
}

// 不变量 3：计数非负、归零即删、Groups 只列非零组且有序。
func TestNonNegativeZeroRemoval(t *testing.T) {
	cases := []struct {
		name, want string
		evs        []api.Event
	}{
		{"归零即删", "", []api.Event{ev(sp("x"), 2), ev(sp("x"), -2)}},
		{"排序NULL最前", "∅=1;=1;a=1;b=1;", []api.Event{ev(sp("b"), 1), ev(nil, 1), ev(sp(""), 1), ev(sp("a"), 1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := api.New(16)
			mustFeed(t, v, tc.evs...)
			if got := snapshotOf(v); got != tc.want {
				t.Fatalf("视图=%q，期望 %q", got, tc.want)
			}
		})
	}
}

// 不变量 4：三类可判定错误互不相同；被拒整批不留痕，之后可正常使用。
func TestFailureAtomicity(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrInvalidParam) {
		t.Fatal("New(0) 未报 ErrInvalidParam")
	}
	bads := []struct {
		name string
		evs  []api.Event
		want error
	}{
		{"计数为负", []api.Event{ev(sp("a"), -2)}, api.ErrNegativeCount},
		{"组名过长", []api.Event{ev(sp("12345678901234567"), 1)}, api.ErrGroupTooLong},
		{"先好后坏同批", []api.Event{ev(sp("a"), 5), ev(nil, -2)}, api.ErrNegativeCount},
	}
	for _, tc := range bads {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := api.New(16)
			mustFeed(t, v, ev(nil, 1), ev(sp("a"), 1))
			before := snapshotOf(v)
			if err := v.Feed(tc.evs); !errors.Is(err, tc.want) {
				t.Fatalf("错误=%v，期望 %v", err, tc.want)
			}
			if snapshotOf(v) != before {
				t.Fatal("被拒批次改变了状态")
			}
			mustFeed(t, v, ev(sp("ok"), 1)) // 之后仍可正常使用
		})
	}
}

// 并发：N 个 goroutine 只读同一实例（含 SelfCheck），视图逐字段相同；无 sleep。
func TestConcurrentReads(t *testing.T) {
	v, _ := api.New(16)
	mustFeed(t, v, ev(nil, 3), ev(sp(""), 1), ev(sp("a"), 2))
	want := snapshotOf(v)
	var wg sync.WaitGroup
	for r := 0; r < 16; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if snapshotOf(v) != want || v.SelfCheck() != nil {
					t.Error("并发读到的视图不一致")
					return
				}
			}
		}()
	}
	wg.Wait()
}
