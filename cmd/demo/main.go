package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ontology/api"
)

var failures []string

func expect(name string, ok bool) {
	if !ok {
		failures = append(failures, name)
	}
}

func setStr(m map[string]bool) string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return "{" + strings.Join(s, ",") + "}"
}

func main() {
	// 第三节七步表：逐步核验到达/离开集、轮次、Released、结果。
	b := api.New(3)
	arrived, departed := map[string]bool{}, map[string]bool{}
	round, released := 0, false
	steps := []struct {
		arrive bool
		id     string
		want   error
	}{{true, "P1", nil}, {true, "P2", nil}, {true, "P3", nil},
		{false, "P1", nil}, {true, "P1", api.ErrArriveAfterDepart},
		{false, "P2", nil}, {false, "P3", nil},
	}
	for i, st := range steps {
		var err error
		if st.arrive {
			err = b.Arrive(st.id)
		} else {
			err = b.Depart(st.id)
		}
		ok := errors.Is(err, st.want) && (err != nil || st.want == nil)
		res := "成功"
		if st.want != nil {
			res = "拒绝"
		} else if st.arrive {
			arrived[st.id] = true
			released = released || len(arrived) == 3
		} else {
			delete(arrived, st.id)
			departed[st.id] = true
			if len(departed) == 3 {
				arrived, departed = map[string]bool{}, map[string]bool{}
				round, released = round+1, false
			}
		}
		ok = ok && b.Round() == round && b.Released() == released
		if st.want != nil { // 失败不留痕：集合也不应变
			ok = ok && !arrived[st.id]
		}
		expect(fmt.Sprintf("step%d", i+1), ok)
		fmt.Printf("%s 步%d %s(%s) 到达=%s 离开=%s 轮次=%d Released=%v 结果=%s\n",
			map[bool]string{true: "OK ", false: "FAIL"}[ok],
			i+1, map[bool]string{true: "Arrive", false: "Depart"}[st.arrive],
			st.id, setStr(arrived), setStr(departed), b.Round(), b.Released(), res)
	}

	// 不变量 1/2：多档 n 下到齐才释放、离开才推进。
	inv12 := true
	for _, n := range []int{1, 3, 50} {
		bb := api.New(n)
		for i := 0; i < n; i++ {
			_ = bb.Arrive(fmt.Sprintf("p%d", i))
			inv12 = inv12 && bb.Released() == (i == n-1)
		}
		for i := 0; i < n; i++ {
			_ = bb.Depart(fmt.Sprintf("p%d", i))
			inv12 = inv12 && (i < n-1) == (bb.Round() == 0)
		}
		inv12 = inv12 && bb.Round() == 1 && !bb.Released()
	}
	expect("inv1+inv2", inv12)

	// 四类可判定错误互不相同 + 被拒后状态不变。
	faults := true
	seen := map[error]bool{}
	for _, tc := range []struct {
		seq  func(*api.Barrier) error
		want error
	}{
		{func(x *api.Barrier) error { return x.Arrive("") }, api.ErrEmptyID},
		{func(x *api.Barrier) error { _ = x.Arrive("a"); return x.Arrive("a") }, api.ErrDuplicateArrive},
		{func(x *api.Barrier) error { return x.Depart("a") }, api.ErrEarlyDepart},
		{func(x *api.Barrier) error {
			_ = x.Arrive("a")
			_ = x.Arrive("b")
			_ = x.Depart("a")
			return x.Arrive("a")
		}, api.ErrArriveAfterDepart}} {
		x := api.New(2)
		err := tc.seq(x)
		r, rel := x.Round(), x.Released()
		faults = faults && errors.Is(err, tc.want) && !seen[tc.want] &&
			errors.Is(tc.seq(x), tc.want) && x.Round() == r && x.Released() == rel
		seen[tc.want] = true
	}
	expect("faults+notrace", faults)

	// 大 m 行为一致（O(1) 登记由 bar 包内 TestRegistryLookupConstant 钉住，计数器不可导出）。
	bigm := true
	for _, m := range []int{100, 1000, 10000} {
		bm := api.New(m)
		for i := 0; i < m; i++ {
			_ = bm.Arrive(fmt.Sprintf("p%d", i))
		}
		bigm = bigm && bm.Released() && errors.Is(bm.Arrive("overflow"), api.ErrRegistryFull)
	}
	expect("bigm", bigm)

	// 并发到齐/离开。
	const cn = 256
	cb := api.New(cn)
	var wg sync.WaitGroup
	for _, op := range []func(string) error{cb.Arrive, cb.Depart} {
		start := make(chan struct{})
		for i := 0; i < cn; i++ {
			wg.Add(1)
			go func(id string) { defer wg.Done(); <-start; _ = op(id) }(fmt.Sprintf("c%d", i))
		}
		close(start)
		wg.Wait()
	}
	expect("concurrent", cb.Round() == 1 && !cb.Released())
	expect("selfcheck", api.New(3).SelfCheck() == nil)

	if len(failures) > 0 {
		fmt.Println("FAIL", strings.Join(failures, ","))
		os.Exit(1)
	}
	fmt.Println("OK  汇总: 到齐才释放/离开才推进/轮次隔离/四类错误/失败不留痕/大m登记/并发/自检")
}
