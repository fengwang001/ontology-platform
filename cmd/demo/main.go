package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/agg"
	"ontology/api"
	"ontology/grp"
)

func fail(msg string) {
	fmt.Println("FAIL " + msg)
	os.Exit(1)
}

func sp(s string) *string             { return &s }
func ev(g *string, d int64) api.Event { return api.Event{Group: g, Delta: d} }

// snapshot 把当前视图格式化为可比较的字符串（解引用指针，只留组名与计数）。
func snapshot(v *api.View) string {
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

func main() {
	// grp：三形态归一
	empty := ""
	a1, a2 := "a", "a"
	if grp.Normalize(nil) == grp.Normalize(&empty) || grp.Normalize(&a1) != grp.Normalize(&a2) {
		fail("grp: 归一化错误")
	}
	fmt.Println("OK grp: nil、空串、普通串三形态互分，同值同键")

	// agg：负值拒绝、归零即删、枚举顺序
	g := agg.New()
	b := "b"
	g.Apply(grp.Normalize(nil), 1)
	g.Apply(grp.Normalize(&empty), 1)
	g.Apply(grp.Normalize(&b), 1)
	g.Apply(grp.Normalize(&b), -1)
	if g.Apply(grp.Normalize(nil), -2) {
		fail("agg: 负计数未拒绝")
	}
	if ks := g.Keys(); len(ks) != 2 || !ks[0].IsNull() || ks[1].Str() != "" {
		fail("agg: 归零未删除或枚举顺序错误")
	}
	fmt.Println("OK agg: 负值拒绝、归零即删、枚举 NULL→空串→字典序")

	// api：第三节八步事件，逐步核验四个组计数与存在集合
	v, err := api.New(16)
	if err != nil {
		fail("api.New: " + err.Error())
	}
	evs := []api.Event{ev(nil, 2), ev(sp(""), 1), ev(sp("a"), 3), ev(nil, 1),
		ev(sp("b"), 5), ev(sp(""), -1), ev(sp("a"), -2), ev(nil, -3)}
	// 每步期望：NULL、""、"a"、"b" 计数 + 存在集合
	want := [][5]any{{2, 0, 0, 0, "{∅}"}, {2, 1, 0, 0, `{∅,""}`}, {2, 1, 3, 0, `{∅,"","a"}`},
		{3, 1, 3, 0, `{∅,"","a"}`}, {3, 1, 3, 5, `{∅,"","a","b"}`}, {3, 0, 3, 5, `{∅,"a","b"}`},
		{3, 0, 1, 5, `{∅,"a","b"}`}, {0, 0, 1, 5, `{"a","b"}`}}
	trace := make([]string, 0, 8)
	for i, e := range evs {
		if err := v.Feed([]api.Event{e}); err != nil {
			fail(fmt.Sprintf("第 %d 步被拒: %v", i+1, err))
		}
		got := []int64{v.Count(nil), v.Count(sp("")), v.Count(sp("a")), v.Count(sp("b"))}
		set := "{"
		for j, gs := range v.Groups() {
			if j > 0 {
				set += ","
			}
			if gs.Group == nil {
				set += "∅"
			} else {
				set += fmt.Sprintf("%q", *gs.Group)
			}
		}
		set += "}"
		w := want[i]
		if got[0] != int64(w[0].(int)) || got[1] != int64(w[1].(int)) ||
			got[2] != int64(w[2].(int)) || got[3] != int64(w[3].(int)) || set != w[4] {
			fail(fmt.Sprintf("第 %d 步: 得 %v %s，期望 %v", i+1, got, set, w))
		}
		trace = append(trace, fmt.Sprintf("%d∅%d\"\"%da%db%d%s", i+1, got[0], got[1], got[2], got[3], set))
	}
	fmt.Println("OK 八步1-4:", trace[0], trace[1], trace[2], trace[3])
	fmt.Println("OK 八步5-8:", trace[4], trace[5], trace[6], trace[7])

	// 三类哨兵错误互不相同且可判定
	if _, err := api.New(0); !errors.Is(err, api.ErrInvalidParam) {
		fail("New(0) 未报 ErrInvalidParam")
	}
	if err := v.Feed([]api.Event{ev(sp("0123456789abcdefg"), 1)}); !errors.Is(err, api.ErrGroupTooLong) {
		fail("超长组名未报 ErrGroupTooLong")
	}
	if err := v.Feed([]api.Event{ev(nil, -1)}); !errors.Is(err, api.ErrNegativeCount) {
		fail("NULL 组已移除后 (nil,-1) 未报 ErrNegativeCount")
	}
	if api.ErrInvalidParam == api.ErrGroupTooLong || api.ErrGroupTooLong == api.ErrNegativeCount {
		fail("哨兵错误不互异")
	}
	fmt.Println("OK 三类哨兵错误可判定且互异（含丙问: 归零后再减报 ErrNegativeCount）")

	// 被拒整批不留痕，之后可正常使用
	before := snapshot(v)
	if err := v.Feed([]api.Event{ev(sp("a"), 5), ev(sp("b"), -99)}); !errors.Is(err, api.ErrNegativeCount) {
		fail("含非法事件的批次未整体拒绝")
	}
	if snapshot(v) != before || v.Count(sp("a")) != 1 {
		fail("被拒批次留下了痕迹")
	}
	if err := v.Feed([]api.Event{ev(sp("c"), 7)}); err != nil || v.Count(sp("c")) != 7 {
		fail("被拒后无法继续正常使用")
	}
	fmt.Println("OK 被拒整批不留痕，之后可正常使用")

	fmt.Println("OK 大 m 定位探查数恒定: 由 agg 包内测试 TestProbeConstant 断言（计数器非导出）")

	// 并发只读：N 个 goroutine 各自读到的视图逐字段相同
	wantGroups := snapshot(v)
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if snapshot(v) != wantGroups || v.Count(sp("a")) != 1 || v.SelfCheck() != nil {
					errs <- "并发读到的视图不一致"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	if e, bad := <-errs; bad {
		fail(e)
	}
	fmt.Println("OK 并发只读视图逐字段一致，SelfCheck 并发安全")
}
