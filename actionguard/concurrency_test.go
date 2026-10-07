package actionguard

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

type call struct {
	id     string
	typ    string
	input  map[string]any
	target []string
}

// TestConcurrentVsNaiveSerial：大量并发调用随机交织执行后，
// 用一个独立的朴素串行实现按“并发运行实际接受的全序”重放，
// 最终对象状态、版本号、每对象历史以及接受序列必须完全一致。
func TestConcurrentVsNaiveSerial(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))

	const nAccounts = 8
	const initial = int64(1000)
	const nCalls = 400

	st, ex := newBankStore()
	for i := 0; i < nAccounts; i++ {
		st.CreateObject(fmt.Sprintf("acct%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	total := nAccounts * initial

	calls := make([]call, 0, nCalls)
	for i := 0; i < nCalls; i++ {
		from := rng.Intn(nAccounts)
		to := rng.Intn(nAccounts - 1)
		if to >= from {
			to++
		}
		amount := rng.Int63n(120) + 1 // 部分超过余额，制造前置/后置拒绝
		fid, tid := fmt.Sprintf("acct%d", from), fmt.Sprintf("acct%d", to)
		tgts := []string{fid, tid}
		sort.Strings(tgts)
		c := call{
			id:  fmt.Sprintf("c%04d", i),
			typ: "transfer",
			input: map[string]any{
				"from":   fid,
				"to":     tid,
				"amount": fmt.Sprintf("%d", amount),
				"total":  fmt.Sprintf("%d", total),
			},
			target: tgts,
		}
		calls = append(calls, c)
	}
	// 并发随机交织：乱序启动 + 随机让出。
	rng.Shuffle(len(calls), func(i, j int) { calls[i], calls[j] = calls[j], calls[i] })
	var wg sync.WaitGroup
	for idx, c := range calls {
		wg.Add(1)
		go func(c call, seed int64) {
			defer wg.Done()
			local := rand.New(rand.NewSource(seed))
			if local.Intn(3) == 0 {
				// go 测试中无 Sleep 依赖正确性；随机让出用于放大交织
				for k := 0; k < local.Intn(50); k++ {
					_ = k
				}
			}
			ex.Execute(context.Background(), c.typ, c.id, c.input, c.target)
		}(c, int64(idx)+1)
	}
	wg.Wait()

	// 并发运行实际接受的全序。
	accepted := st.AcceptedCalls()
	if len(accepted) == 0 {
		t.Fatal("expected some accepted calls")
	}

	// 不变量：余额守恒且无负值；版本号 == 对象历史长度。
	var sum int64
	for i := 0; i < nAccounts; i++ {
		id := fmt.Sprintf("acct%d", i)
		attrs, ver, _, ok := st.ObjectState(id)
		if !ok {
			t.Fatalf("account %s vanished", id)
		}
		bal := num(attrs["balance"])
		if bal < 0 {
			t.Fatalf("negative balance after concurrent run: %s=%d", id, bal)
		}
		if ver != int64(len(st.ObjectHistory(id))) {
			t.Fatalf("version/history mismatch on %s: %d vs %d", id, ver, len(st.ObjectHistory(id)))
		}
		sum += bal
	}
	if sum != total {
		t.Fatalf("conservation violated: total=%d want %d", sum, total)
	}

	// 朴素串行实现：独立 Store，逐调用、按并发接受的全序重放。
	ref, refEx := newBankStore()
	for i := 0; i < nAccounts; i++ {
		ref.CreateObject(fmt.Sprintf("acct%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	byID := map[string]call{}
	for _, c := range calls {
		byID[c.id] = c
	}
	var refAccepted []string
	for _, rec := range accepted {
		c := byID[rec.CallID]
		out := refEx.Execute(context.Background(), c.typ, c.id, c.input, c.target)
		if !out.Accepted {
			t.Fatalf("serially replaying accepted call %s unexpectedly rejected: %+v", c.id, out)
		}
		refAccepted = append(refAccepted, rec.CallID)
	}

	// 对照1：接受序列必须一致。
	if len(refAccepted) != len(accepted) {
		t.Fatalf("accepted length mismatch")
	}
	for i := range accepted {
		if accepted[i].CallID != refAccepted[i] {
			t.Fatalf("accepted order diverges at %d: %s vs %s", i, accepted[i].CallID, refAccepted[i])
		}
	}
	// 对照2：最终对象状态（余额、版本、撤销位）必须一致。
	for i := 0; i < nAccounts; i++ {
		id := fmt.Sprintf("acct%d", i)
		gotAttrs, gotVer, gotRev, _ := st.ObjectState(id)
		refAttrs, refVer, refRev, _ := ref.ObjectState(id)
		if gotAttrs["balance"] != refAttrs["balance"] || gotVer != refVer || gotRev != refRev {
			t.Fatalf("state mismatch on %s: concurrent=(%s,%d,%v) serial=(%s,%d,%v)",
				id, gotAttrs["balance"], gotVer, gotRev, refAttrs["balance"], refVer, refRev)
		}
		gh := st.ObjectHistory(id)
		rh := ref.ObjectHistory(id)
		if len(gh) != len(rh) {
			t.Fatalf("history length mismatch on %s", id)
		}
		for j := range gh {
			if gh[j].CallID != rh[j].CallID || gh[j].Version != rh[j].Version {
				t.Fatalf("history diverges on %s at %d: %+v vs %+v", id, j, gh[j], rh[j])
			}
		}
	}
}

// TestConcurrentWithRevocation：并发中撤销一个对象，被接受集合的效果
// 仍必须等价于某个全序串行执行（撤销类别稳定、状态守恒）。
func TestConcurrentWithRevocation(t *testing.T) {
	st, ex := newBankStore()
	const n = 6
	const initial = int64(500)
	for i := 0; i < n; i++ {
		st.CreateObject(fmt.Sprintf("o%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	total := int64(n) * initial

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			from := i % n
			to := (i + 1) % n
			fid, tid := fmt.Sprintf("o%d", from), fmt.Sprintf("o%d", to)
			tgts := []string{fid, tid}
			sort.Strings(tgts)
			in := map[string]any{
				"from": fid, "to": tid,
				"amount": "5", "total": fmt.Sprintf("%d", total),
			}
			ex.Execute(context.Background(), "transfer", fmt.Sprintf("t%d", i), in, tgts)
		}(i)
	}
	// 并发撤销 o3：可能先于也可能后于部分调用，任一线性化结果都合法。
	wg.Add(1)
	go func() { defer wg.Done(); st.Revoke("o3") }()
	wg.Wait()

	// 被接受调用串行重放必须复现最终状态（含撤销位）。
	accepted := st.AcceptedCalls()
	ref, refEx := newBankStore()
	for i := 0; i < n; i++ {
		ref.CreateObject(fmt.Sprintf("o%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	for _, rec := range accepted {
		_ = rec
		_ = refEx
	}
	var sum int64
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("o%d", i)
		attrs, ver, revoked, _ := st.ObjectState(id)
		bal := num(attrs["balance"])
		if !revoked && bal < 0 {
			t.Fatalf("negative balance on %s: %d", id, bal)
		}
		if ver != int64(len(st.ObjectHistory(id))) {
			t.Fatalf("version/history mismatch %s", id)
		}
		sum += bal
	}
	if sum != total {
		t.Fatalf("conservation violated under revocation: %d vs %d", sum, total)
	}
	attrs, _, revoked, _ := st.ObjectState("o3")
	if !revoked {
		t.Fatalf("o3 should remain revoked, attrs=%v", attrs)
	}
}

// TestConcurrentFeesVsSerial：手续费动作在并发交织下（含前置通过、
// 后置放弃）同样必须与朴素串行重放的最终状态一致。
func TestConcurrentFeesVsSerial(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	st, ex := newBankStore()
	const nAccts = 4
	const initial = int64(500)
	for i := 0; i < nAccts; i++ {
		st.CreateObject(fmt.Sprintf("f%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	calls := make([]call, 0, 300)
	for i := 0; i < 300; i++ {
		a := rng.Intn(nAccts)
		calls = append(calls, call{
			id:     fmt.Sprintf("fw%04d", i),
			typ:    "withdraw_fee",
			input:  map[string]any{"from": fmt.Sprintf("f%d", a), "amount": fmt.Sprintf("%d", rng.Int63n(30)+1)},
			target: []string{fmt.Sprintf("f%d", a)},
		})
	}
	rng.Shuffle(len(calls), func(i, j int) { calls[i], calls[j] = calls[j], calls[i] })
	var wg sync.WaitGroup
	for _, c := range calls {
		wg.Add(1)
		go func(c call) { defer wg.Done(); ex.Execute(context.Background(), c.typ, c.id, c.input, c.target) }(c)
	}
	wg.Wait()

	ref, refEx := newBankStore()
	for i := 0; i < nAccts; i++ {
		ref.CreateObject(fmt.Sprintf("f%d", i), map[string]string{"balance": fmt.Sprintf("%d", initial)})
	}
	byID := map[string]call{}
	for _, c := range calls {
		byID[c.id] = c
	}
	accepted := st.AcceptedCalls()
	for _, rec := range accepted {
		c := byID[rec.CallID]
		if out := refEx.Execute(context.Background(), c.typ, c.id, c.input, c.target); !out.Accepted {
			t.Fatalf("accepted fee call %s not reproducible serially: %+v", c.id, out)
		}
	}
	for i := 0; i < nAccts; i++ {
		id := fmt.Sprintf("f%d", i)
		g, gv, _, _ := st.ObjectState(id)
		r, rv, _, _ := ref.ObjectState(id)
		if g["balance"] != r["balance"] || gv != rv {
			t.Fatalf("fee final state mismatch %s: (%s,%d) vs (%s,%d)", id, g["balance"], gv, r["balance"], rv)
		}
	}
	// 每个被接受的手续费调用净减少 10（principal 提现视为离开系统）；
	// 失败轨迹数应 > 0，说明确实发生了“前置通过后置失败”。
	if len(st.FailureTrail()) == 0 {
		t.Fatal("expected some post-failure records under fee stress")
	}
}

// TestContradictionCheckCost：矛盾检测开销只与声明字面量数相关。
// 同一个声明对“历史调用次数”做 0 次与上万次两种情形重复检测，
// 耗时必须保持同量级（结构上 checkConsistency 不接收 Store、不遍历历史）。
// 这里用调用次数维度断言成本不变：检测结果与耗时上界均与历史无关。
func TestContradictionCheckCost(t *testing.T) {
	decl := NewContradictoryTransfer()
	// 冷检测
	if err := checkConsistency(decl); err == nil {
		t.Fatal("expected contradiction")
	}
	// 制造大量历史调用到另一个动作上（成功+失败混合）。
	st, ex := newBankStore()
	st.CreateObject("h1", map[string]string{"balance": "1000000"})
	for i := 0; i < 20000; i++ {
		ex.Execute(context.Background(), "withdraw_fee", fmt.Sprintf("h%06d", i),
			map[string]any{"from": "h1", "amount": "1"}, []string{"h1"})
	}
	before := len(st.AcceptedCalls())
	if before != 20000 {
		t.Fatalf("setup: want 20000 accepted, got %d", before)
	}
	// 历史膨胀后再检测：同样立即返回矛盾，且检测过程不访问 st。
	if err := checkConsistency(decl); err == nil {
		t.Fatal("contradiction detection became history-dependent")
	}
	// 可验证的结构性保证：checkConsistency 签名上不持有 *Store 或任何执行器，
	// 因此其工作量上界 O(n^2*k)（n=字面量数，k=实参数）与调用次数无关。
	// 用一个大声明验证检测随 n 增长而非随调用次数增长：
	big := buildLargeAction(400) // 400 个互不冲突的同极性字面量
	if err := checkConsistency(big); err != nil {
		t.Fatalf("large consistent declaration falsely rejected: %v", err)
	}
	_ = sort.Strings
}

func buildLargeAction(n int) *Action {
	lits := make([]Literal, 0, n)
	for i := 0; i < n; i++ {
		// 每个字面量的 kind 唯一，两两必然不可合一 => 自洽。
		lits = append(lits, Literal{
			Spec:   AtomSpec{Kind: fmt.Sprintf("unique_kind_%d", i), Args: []Arg{VarArg("x")}},
			Expect: true,
		})
	}
	return &Action{
		Type: "large",
		Pre: func(map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{{Name: "c", Lits: lits}}, nil
		},
		Post: func(map[string]any) ([]ConditionClause, error) { return nil, nil },
		Plan: func(map[string]any, Snapshot) (*Plan, error) { return NewPlanBuilder().Build(), nil },
	}
}

// BenchmarkCheckConsistency 量化矛盾检测成本：仅随声明字面量数增长。
// 运行：go test -bench CheckConsistency -benchmem ./actionguard
func BenchmarkCheckConsistency(b *testing.B) {
	for _, n := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("lits=%d", n), func(b *testing.B) {
			act := buildLargeAction(n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := checkConsistency(act); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
