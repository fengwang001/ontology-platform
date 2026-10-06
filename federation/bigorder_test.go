package federation

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// 7. 极大数值：10^15 量级（以及 10^30 压力值）下 big.Int 不溢出、总和恰好。
func TestHugeValues(t *testing.T) {
	l := newTestLog(t, "huge")
	defer l.close()
	e15 := new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)
	e30 := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	mk := func(name string, w, min, capv *big.Int) *Cluster {
		return &Cluster{Name: name, Weight: w, Min: min, Max: nil,
			Capacity: capv, Available: true, Current: big.NewInt(0)}
	}
	reg := NewRegistry()
	for _, c := range []*Cluster{
		mk("a", new(big.Int).Set(e15), big.NewInt(0), new(big.Int).Set(e30)),
		mk("b", new(big.Int).Mul(e15, big.NewInt(2)), big.NewInt(0), new(big.Int).Set(e30)),
		mk("c", new(big.Int).Mul(e15, big.NewInt(3)), new(big.Int).Set(e15), new(big.Int).Set(e30)),
	} {
		if err := reg.Register(c); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	l.printf("[huge] 输入 total=10^30, 权重 1e15/2e15/3e15, c.min=1e15, 容量 1e30")
	res, err := reg.Allocate(new(big.Int).Set(e30))
	logResult(l, "huge", res, err)
	if err != nil {
		t.Fatalf("极大数值分配不应失败: %v", err)
	}
	sum := big.NewInt(0)
	for _, v := range res.Targets {
		sum.Add(sum, v)
	}
	if sum.Cmp(e30) != 0 {
		t.Fatalf("判定依据: 目标之和必须恰好等于 10^30，得到 %s", sum)
	}
	// 权重比 1:2:3，c 先拿 min=1e15；剩余 10^30-1e15 按 1:2:3 分摊。
	rem := new(big.Int).Sub(e30, e15)
	wantA := new(big.Int).Quo(rem, big.NewInt(6))
	wantB := new(big.Int).Quo(new(big.Int).Mul(rem, big.NewInt(2)), big.NewInt(6))
	wantC := new(big.Int).Add(e15,
		new(big.Int).Quo(new(big.Int).Mul(rem, big.NewInt(3)), big.NewInt(6)))
	// 向下取整总偏差小于参与集群数，余下副本按小数分配，这里只校验严格不变量：
	for name, want := range map[string]*big.Int{"a": wantA, "b": wantB} {
		diff := new(big.Int).Sub(res.Targets[name], want)
		if diff.Sign() < 0 || diff.Cmp(big.NewInt(6)) > 0 {
			t.Fatalf("%s 的份额偏离 floor 值 %s，diff=%s", name, want, diff)
		}
	}
	if res.Targets["c"].Cmp(wantC) < 0 {
		t.Fatalf("c 至少应拿到 min + floor(3*rem/6)=%s，实际 %s", wantC, res.Targets["c"])
	}
	l.printf("判定: PASS，10^30 下 big.Int 精确、目标之和恰好等于 total")
}

// 8. 输入顺序无关：打乱登记顺序，结果必须逐项相同；变更计划也按名称排序。
func TestOrderIndependent(t *testing.T) {
	l := newTestLog(t, "order")
	defer l.close()
	base := []naiveInput{
		{name: "n0", weight: 3, min: 1, max: 8, capacity: 8, available: true, current: 2},
		{name: "n1", weight: 7, min: 0, max: -1, capacity: 30, available: true, current: 9},
		{name: "n2", weight: 0, min: 2, max: -1, capacity: 5, available: true, current: 2},
		{name: "n3", weight: 5, min: 1, max: 6, capacity: 10, available: false, current: 4},
		{name: "n4", weight: 1, min: 0, max: 3, capacity: 3, available: true, current: 0},
	}
	shuffled := make([]naiveInput, len(base))
	copy(shuffled, base)
	rand.New(rand.NewSource(42)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	logInput(l, "order-a", base, 20)
	logInput(l, "order-b", shuffled, 20)
	r1 := mustAlloc(t, regFromNaive(t, base), 20)
	r2 := mustAlloc(t, regFromNaive(t, shuffled), 20)
	logResult(l, "order-a", r1, nil)
	logResult(l, "order-b", r2, nil)
	for _, name := range []string{"n0", "n1", "n2", "n3", "n4"} {
		if r1.Targets[name].Cmp(r2.Targets[name]) != 0 {
			t.Fatalf("判定依据: %s 目标随输入顺序变化: %s vs %s",
				name, r1.Targets[name], r2.Targets[name])
		}
	}
	if len(r1.Changes) != len(r2.Changes) {
		t.Fatalf("变更计划长度随输入顺序变化")
	}
	for i := range r1.Changes {
		if r1.Changes[i].Name != r2.Changes[i].Name {
			t.Fatalf("变更计划未按名称排序/随顺序变化: %s vs %s",
				r1.Changes[i].Name, r2.Changes[i].Name)
		}
	}
	if r1.Migration.Cmp(r2.Migration) != 0 {
		t.Fatalf("迁移量随输入顺序变化: %s vs %s", r1.Migration, r2.Migration)
	}
	l.printf("判定: PASS，打乱登记顺序后目标、变更计划、迁移量完全一致")
}

// 稳定性：非并列场景下当前承载数不得影响目标。
func TestCurrentDoesNotAffectTargets(t *testing.T) {
	l := newTestLog(t, "stability")
	defer l.close()
	makeIn := func(cur []int64) []naiveInput {
		in := []naiveInput{}
		for i, c := range cur {
			in = append(in, naiveInput{
				name: fmt.Sprintf("s%d", i), weight: int64(2 + i), min: 0, max: -1,
				capacity: 1000, available: true, current: c})
		}
		return in
	}
	in1 := makeIn([]int64{0, 0, 0})
	in2 := makeIn([]int64{100, 7, 999})
	logInput(l, "stable-1", in1, 37)
	logInput(l, "stable-2", in2, 37)
	r1 := mustAlloc(t, regFromNaive(t, in1), 37)
	r2 := mustAlloc(t, regFromNaive(t, in2), 37)
	logResult(l, "stable-1", r1, nil)
	logResult(l, "stable-2", r2, nil)
	for name := range r1.Targets {
		if r1.Targets[name].Cmp(r2.Targets[name]) != 0 {
			t.Fatalf("判定依据: 无小数并列时 current 影响了 %s 的目标: %s vs %s",
				name, r1.Targets[name], r2.Targets[name])
		}
	}
	l.printf("判定: PASS，除小数并列外 current 完全不影响目标副本数")
}
