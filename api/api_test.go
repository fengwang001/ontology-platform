package api_test

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
	"ontology/drf"
)

func fr(n, d int64) drf.Frac { return drf.Ratio(n, d) }
func fs(x drf.Frac) string   { return strconv.FormatInt(x.N, 10) + "/" + strconv.FormatInt(x.D, 10) }

// TestSentinelErrorsAreDistinct 五类错误可判定且互不相同。
func TestSentinelErrorsAreDistinct(t *testing.T) {
	s := []error{api.ErrEmptyID, api.ErrDuplicateID, api.ErrNegativeDemand, api.ErrZeroDemand, api.ErrInvalidCapacity}
	for i, e := range s {
		if !errors.Is(e, e) {
			t.Fatalf("哨兵 %d 不能判定自身", i)
		}
		for j := 0; j < i; j++ {
			if errors.Is(e, s[j]) {
				t.Fatalf("哨兵 %d 与 %d 不互异", i, j)
			}
		}
	}
}

// TestRejectedOperationsLeaveNoTrace 不变量4：拒绝返回对应哨兵，状态不变，仍可继续使用。
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	type tc struct {
		name             string
		id               string
		cpu, mem, cC, cM int64
		new              bool
		want             error
	}
	cases := []tc{
		{"empty-id", "", 1, 1, 0, 0, false, api.ErrEmptyID},
		{"dup-id", "A", 1, 1, 0, 0, false, api.ErrDuplicateID},
		{"neg-cpu", "x", -1, 1, 0, 0, false, api.ErrNegativeDemand},
		{"neg-mem", "x", 1, -1, 0, 0, false, api.ErrNegativeDemand},
		{"zero-both", "x", 0, 0, 0, 0, false, api.ErrZeroDemand},
		{"bad-cap-cpu", "", 0, 0, 0, 60, true, api.ErrInvalidCapacity},
		{"bad-cap-mem", "", 0, 0, 60, 0, true, api.ErrInvalidCapacity},
	}
	for _, c := range cases {
		a, err := api.New(60, 60)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := a.Add("A", 1, 6); err != nil {
			t.Fatalf("%s: 预置 Add 失败", c.name)
		}
		before := a.Allocate()
		if c.new {
			if _, err := api.New(c.cC, c.cM); !errors.Is(err, c.want) {
				t.Fatalf("%s: %v want %v", c.name, err, c.want)
			}
		} else if err := a.Add(c.id, c.cpu, c.mem); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v want %v", c.name, err, c.want)
		}
		after := a.Allocate() // 拒绝前后逐 id 相同
		if len(after) != len(before) {
			t.Fatalf("%s: 拒绝后任务数 %d→%d", c.name, len(before), len(after))
		}
		for id, v := range before {
			if drf.Cmp(after[id], v) != 0 {
				t.Fatalf("%s: 拒绝后 %s 分配变化", c.name, id)
			}
		}
		if a.Add("late", 2, 2) != nil || a.SelfCheck() != nil { // 仍可正常使用/自检
			t.Fatalf("%s: 被拒后分配器不可继续使用", c.name)
		}
	}
}

// TestConcurrentAdd N 个 goroutine 各 Add 不同 id（随机乱序、无 sleep），
// 结果与顺序 Add 逐 id 一致且满足两条硬约束。
func TestConcurrentAdd(t *testing.T) {
	rng := rand.New(rand.NewSource(918651))
	for _, n := range []int{2, 16, 128} {
		type d struct {
			id       string
			cpu, mem int64
		}
		ds := make([]d, n)
		for i := range ds {
			c, m := int64(1+rng.Intn(9)), int64(1+rng.Intn(9))
			ds[i] = d{"g" + strconv.Itoa(i), c, m}
		}
		got, _ := api.New(200, 200) // 并发 Add，随机置换决定顺序
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, idx := range rng.Perm(n) {
			wg.Add(1)
			go func(x d) {
				defer wg.Done()
				<-start // 同时放行，最大化交错
				if err := got.Add(x.id, x.cpu, x.mem); err != nil {
					t.Error(err)
				}
			}(ds[idx])
		}
		close(start)
		wg.Wait()
		ga := got.Allocate()
		want, _ := api.New(200, 200) // 顺序参照
		for _, x := range ds {
			if err := want.Add(x.id, x.cpu, x.mem); err != nil {
				t.Fatal(err)
			}
		}
		wa, uC, uM := want.Allocate(), fr(0, 1), fr(0, 1)
		if len(ga) != n || len(wa) != n {
			t.Fatalf("n=%d 任务数 并发=%d 顺序=%d", n, len(ga), len(wa))
		}
		for _, x := range ds {
			if drf.Cmp(ga[x.id], wa[x.id]) != 0 {
				t.Fatalf("n=%d id=%s 并发=%s 顺序=%s", n, x.id, fs(ga[x.id]), fs(wa[x.id]))
			}
			uC = drf.Add(uC, drf.Mul(ga[x.id], fr(x.cpu, 1)))
			uM = drf.Add(uM, drf.Mul(ga[x.id], fr(x.mem, 1)))
		}
		if drf.Cmp(uC, fr(200, 1)) > 0 || drf.Cmp(uM, fr(200, 1)) > 0 {
			t.Fatalf("n=%d 硬约束突破 cpu=%s mem=%s", n, fs(uC), fs(uM))
		}
	}
}

// TestSelfCheck 全新分配器 SelfCheck 通过且不改变自身状态。
func TestSelfCheck(t *testing.T) {
	a, err := api.New(60, 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if r := a.Allocate(); len(r) != 0 {
		t.Fatalf("SelfCheck 污染状态: %v", r)
	}
	if _, err := api.New(-1, 1); !errors.Is(err, api.ErrInvalidCapacity) {
		t.Fatalf("非法容量 %v", err)
	}
}
