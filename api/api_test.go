package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/agg"
	"ontology/api"
	"reflect"
	"sync"
	"testing"
)

func sp(s string) *string { return &s }

type feedCase struct { // 表驱动用例行
	evs    []api.Event
	reject bool
	exist  int
	want   error
}

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

// 不变量 1：任意事件序列与朴素批量重算逐组一致（多档规模、随机顺序、含拒绝）。
func TestBatchEquivalence(t *testing.T) {
	keys := []string{"", "a", "b", "c", "d", "e", "f", "g"} // 已是枚举序
	for _, n := range []int{1, 8, 100, 1000} {
		rng := rand.New(rand.NewSource(int64(n)))
		v, _ := api.New(16)
		model := map[string]int64{} // 键 "NULL" 表示 nil 组
		for i := 0; i < n; i++ {
			g, d := sp(keys[rng.Intn(len(keys))]), int64(rng.Intn(8)-3)
			if rng.Intn(4) == 0 { // 1/4 概率 NULL 组
				g = nil
			}
			k := "NULL"
			if g != nil {
				k = *g
			}
			reject := model[k]+d < 0
			if err := v.Feed([]api.Event{{Group: g, Delta: d}}); (err != nil) != reject {
				t.Fatalf("n=%d 步%d: 拒收判定与模型不符 err=%v", n, i, err)
			}
			if !reject {
				model[k] += d
			}
		}
		want, got, exp := []api.GroupState{}, "", ""
		for _, k := range append([]string{"NULL"}, keys...) {
			g := sp(k)
			if k == "NULL" {
				g = nil
			}
			got += fmt.Sprintf("%d;", v.Count(g))
			exp += fmt.Sprintf("%d;", model[k])
			if model[k] > 0 {
				want = append(want, api.GroupState{Group: g, Count: model[k]})
			}
		}
		if got != exp || !reflect.DeepEqual(v.Groups(), want) {
			t.Fatalf("n=%d: 计数 %s 应 %s; Groups=%v 应 %v", n, got, exp, v.Groups(), want)
		}
	}
}

// 不变量 2：NULL 组与空串组互不影响。
func TestNullVsEmpty(t *testing.T) {
	v, _ := api.New(16)
	must(t, v.Feed([]api.Event{{Group: nil, Delta: 2}, {Group: sp(""), Delta: 1}}))
	ok := v.Count(nil) == 2 && v.Count(sp("")) == 1 // 各自独立计数
	must(t, v.Feed([]api.Event{{Group: sp(""), Delta: -1}}))
	if !ok || v.Count(nil) != 2 || v.Count(sp("")) != 0 || len(v.Groups()) != 1 {
		t.Fatal("空串归零不应影响 NULL 组")
	}
}

// 不变量 3：计数非负、归零即删。
func TestNonNegativeZeroRemoval(t *testing.T) {
	v, _ := api.New(16)
	steps := []feedCase{
		{evs: []api.Event{{Group: sp("a"), Delta: 1}}, exist: 1},
		{evs: []api.Event{{Group: sp("a"), Delta: -1}}},               // 归零即删
		{evs: []api.Event{{Group: sp("a"), Delta: -1}}, reject: true}, // 不存在再减 → 拒
	}
	for i, s := range steps {
		if err := v.Feed(s.evs); (err != nil) != s.reject || len(v.Groups()) != s.exist {
			t.Fatalf("步%d: err=%v 存在组数=%d 应 %d", i, err, len(v.Groups()), s.exist)
		}
	}
}

// 不变量 4：三类可判定错误互不相同，被拒整批不留痕，之后可正常使用。
func TestFailureAtomicity(t *testing.T) {
	for _, bad := range []int{0, -7} {
		if _, err := api.New(bad); !errors.Is(err, api.ErrBadParam) {
			t.Fatalf("New(%d) 应报 ErrBadParam, got %v", bad, err)
		}
	}
	v, _ := api.New(4)
	must(t, v.Feed([]api.Event{{Group: sp("ab"), Delta: 3}}))
	snap := v.Groups()
	cases := []feedCase{
		{evs: []api.Event{{Group: sp("ab"), Delta: -4}}, want: agg.ErrNegative},
		{evs: []api.Event{{Group: sp("xy"), Delta: 9}, {Group: sp("ab"), Delta: -4}}, want: agg.ErrNegative},
		{evs: []api.Event{{Group: sp("abcde"), Delta: 1}}, want: api.ErrTooLong},
	}
	for i, c := range cases {
		if err := v.Feed(c.evs); !errors.Is(err, c.want) || !reflect.DeepEqual(v.Groups(), snap) {
			t.Fatalf("例%d: err=%v 应 %v，且状态不得改变", i, err, c.want)
		}
	}
	must(t, v.Feed([]api.Event{{Group: sp("xy"), Delta: 1}})) // 被拒后可正常使用
}

func TestConcurrentReads(t *testing.T) {
	v, _ := api.New(16)
	var evs []api.Event
	for i := 0; i < 100; i++ {
		evs = append(evs, api.Event{Group: sp(fmt.Sprintf("g%03d", i)), Delta: int64(i + 1)})
	}
	must(t, v.Feed(append(evs, api.Event{Group: nil, Delta: 7})))
	snap := v.Groups()
	start, out := make(chan struct{}), make(chan bool, 16)
	var wg sync.WaitGroup
	wg.Add(16)
	for i := 0; i < 16; i++ {
		go func() {
			defer wg.Done()
			<-start
			if v.SelfCheck() != nil {
				t.Error("并发 SelfCheck 失败")
			}
			out <- reflect.DeepEqual(v.Groups(), snap)
		}()
	}
	close(start)
	wg.Wait()
	close(out)
	for ok := range out {
		if !ok {
			t.Fatal("并发只读视图不一致")
		}
	}
}
