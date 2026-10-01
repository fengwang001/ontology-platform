package wallet

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Reason
	}
	return Reason("unknown")
}

func equalDeducts(a, b []DeductItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalRefunds(a, b []RefundItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustGrant(t *testing.T, w *Wallet, id, amount, exp, now int64) {
	t.Helper()
	if err := w.Grant(id, amount, exp, now); err != nil {
		t.Fatalf("Grant(%d,%d,%d,%d) 被拒: %v", id, amount, exp, now, err)
	}
}

// TestExpiresAtExactExp：时刻恰等于 exp 时批次已过期（左闭右开）。
func TestExpiresAtExactExp(t *testing.T) {
	w := New()
	t.Log("输入: Grant(id=1, 数量=10, exp=5, now=1)")
	mustGrant(t, w, 1, 10, 5, 1)

	// now 必须单调不减，故先查 now=4（有效），再查 now=5（恰过期）。
	got := w.Balance(4)
	t.Logf("输出: Balance(now=4)=%d；判定依据: now<exp 仍有效, 应为 10", got)
	if got != 10 {
		t.Fatalf("now<exp 时余额应=10, 实际 %d", got)
	}

	got = w.Balance(5)
	t.Logf("输出: Balance(now=5)=%d；判定依据: 批次在 [1,5) 有效, now==5 已过期, 应为 0", got)
	if got != 0 {
		t.Fatalf("now==exp 时余额应=0, 实际 %d", got)
	}

	_, err := w.Spend(100, 1, 5)
	t.Logf("输出: Spend(sid=100, 数量=1, now=5) err=%v；判定依据: 无有效批次, 应拒绝(余额不足)", err)
	if reasonOf(err) != ReasonInsufficientBalance {
		t.Fatalf("应拒绝余额不足, 实际 %v", err)
	}
}

// TestSameExpiryGrantOrder：到期时刻相同时按发放先后消费。
func TestSameExpiryGrantOrder(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 5, 10, 1)
	mustGrant(t, w, 2, 5, 10, 2)
	mustGrant(t, w, 3, 10, 8, 2) // 数量 10、exp=8，应最优先

	items, err := w.Spend(1, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("输入: Spend(sid=1, 数量=10, now=3)")
	t.Logf("输出: %v；判定依据: exp=8 的批次3 最优先, 应全部从批次3扣(10)", items)
	if !equalDeducts(items, []DeductItem{{3, 10}}) {
		t.Fatalf("扣减明细不符: %v", items)
	}

	items, err = w.Spend(2, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("输入: Spend(sid=2, 数量=7, now=3)")
	t.Logf("输出: %v；判定依据: 批次1、2 同为 exp=10, 按发放先后先扣批次1 的5 再扣批次2 的2", items)
	if !equalDeducts(items, []DeductItem{{1, 5}, {2, 2}}) {
		t.Fatalf("同 exp 未按发放先后: %v", items)
	}
}

// TestSpendAcrossBatches：一笔消费跨多批。
func TestSpendAcrossBatches(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 3, 4, 1)
	mustGrant(t, w, 2, 5, 6, 2)
	mustGrant(t, w, 3, 7, 9, 3)

	items, err := w.Spend(1, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []DeductItem{{1, 3}, {2, 5}, {3, 2}}
	t.Log("输入: Spend(sid=1, 数量=10, now=3)；批次1剩3(exp4)、批次2剩5(exp6)、批次3剩7(exp9)")
	t.Logf("输出: %v；判定依据: 按 exp 升序逐批扣减, 期望 %v", items, want)
	if !equalDeducts(items, want) {
		t.Fatalf("跨批扣减不符: got %v want %v", items, want)
	}
	if bal := w.Balance(3); bal != 5 {
		t.Fatalf("剩余余额应=5, 实际 %d", bal)
	}
}

// TestRefundReverseOrder：退回逆序，先退最后扣的批次；部分退回后再退剩余。
func TestRefundReverseOrder(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 3, 10, 1)
	mustGrant(t, w, 2, 5, 10, 2)
	mustGrant(t, w, 3, 7, 10, 3)

	items, err := w.Spend(1, 10, 4) // 扣减次序: 1(3), 2(5), 3(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Spend(sid=1, 数量=10, now=4) 输出: %v", items)

	r1, err := w.Refund(1, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	want1 := []RefundItem{{3, 2, false}, {2, 2, false}}
	t.Log("输入: Refund(sid=1, 数量=4, now=4)")
	t.Logf("输出: %v；判定依据: 逆扣减次序, 先退批次3 的2 再退批次2 的2, 期望 %v", r1, want1)
	if !equalRefunds(r1, want1) {
		t.Fatalf("首次退回不符: got %v want %v", r1, want1)
	}

	r2, err := w.Refund(1, 6, 4) // 退回剩余全部
	if err != nil {
		t.Fatal(err)
	}
	want2 := []RefundItem{{2, 3, false}, {1, 3, false}}
	t.Log("输入: Refund(sid=1, 数量=6, now=4)（退剩余）")
	t.Logf("输出: %v；判定依据: 剩余可退为批次2 的3、批次1 的3, 仍按逆序, 期望 %v", r2, want2)
	if !equalRefunds(r2, want2) {
		t.Fatalf("二次退回不符: got %v want %v", r2, want2)
	}

	_, err = w.Refund(1, 1, 4)
	t.Logf("输入: Refund(sid=1, 数量=1, now=4) 输出 err=%v；判定依据: 无可退数量, 应拒绝(超量)", err)
	if reasonOf(err) != ReasonRefundTooLarge {
		t.Fatalf("应拒绝超量, 实际 %v", err)
	}
	if bal := w.Balance(4); bal != 15 {
		t.Fatalf("全部退回后余额应=15, 实际 %d", bal)
	}
}

// TestRefundExpiredBatchVoided：退回时目标批已过期，作废并计入过期丢弃。
func TestRefundExpiredBatchVoided(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 3, 5, 1)  // 早到期
	mustGrant(t, w, 2, 5, 20, 2) // 晚到期

	items, err := w.Spend(1, 8, 3) // 扣 1(3)、2(5)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Spend(sid=1, 数量=8, now=3) 输出: %v（先扣批次1 的3、再扣批次2 的5）", items)

	// now=5：批次1 恰过期；批次2 仍有效。
	r, err := w.Refund(1, 8, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []RefundItem{{2, 5, false}, {1, 3, true}}
	t.Log("输入: Refund(sid=1, 数量=8, now=5)；批次1 exp=5 此刻已过期, 批次2 仍有效")
	t.Logf("输出: %v；判定依据: 逆序先退批次2 的5(入账), 再退批次1 的3(作废), 期望 %v", r, want)
	if !equalRefunds(r, want) {
		t.Fatalf("作废退回不符: got %v want %v", r, want)
	}

	if d := w.Discarded(); d != 3 {
		t.Fatalf("过期丢弃应=3, 实际 %d", d)
	}
	if bal := w.Balance(5); bal != 5 {
		t.Fatalf("作废部分不入余额, 余额应=5, 实际 %d", bal)
	}
	t.Logf("输出: Discarded=%d, Balance(5)=%d；判定依据: 批次1 的3 作废, 仅批次2 退回的5 入账",
		w.Discarded(), w.Balance(5))

	// 恒等式：remain + deducted - refunded == 发放量，即使有作废也成立。
	for _, bs := range w.Batches(5) {
		if got := bs.Remaining + bs.Deducted - bs.Refunded; got != bs.Granted {
			t.Fatalf("批次%d 恒等式破坏: %d+%d-%d != %d",
				bs.ID, bs.Remaining, bs.Deducted, bs.Refunded, bs.Granted)
		}
	}
}

// TestInsufficientBalanceNoChange：余额不足时整体拒绝且零改动。
func TestInsufficientBalanceNoChange(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 4, 10, 1)
	mustGrant(t, w, 2, 4, 10, 2)

	before := w.Batches(2)
	_, err := w.Spend(99, 9, 2)
	t.Log("输入: Spend(sid=99, 数量=9, now=2)；有效余额 8")
	t.Logf("输出: err=%v；判定依据: 有效余额不足, 整体拒绝, 批次与消费单零改动", err)
	if reasonOf(err) != ReasonInsufficientBalance {
		t.Fatalf("应拒绝余额不足, 实际 %v", err)
	}
	after := w.Batches(2)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("拒绝后批次发生改动: before=%+v after=%+v", before[i], after[i])
		}
	}
	// 被拒的 sid 不应占用：后续可用同一 sid 成功消费。
	if _, err := w.Spend(99, 8, 2); err != nil {
		t.Fatalf("被拒的 sid 应可复用: %v", err)
	}
}

// TestNowRegression：now 回退被拒且不改时间线。
func TestNowRegression(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 10, 20, 5)

	err := w.Grant(2, 10, 20, 4)
	t.Log("输入: 此前最后 now=5, Grant(id=2,...,now=4)")
	t.Logf("输出: err=%v；判定依据: now 回退, 第一个被报的拒绝原因即 now regression", err)
	if reasonOf(err) != ReasonNowRegression {
		t.Fatalf("应拒绝 now 回退, 实际 %v", err)
	}
	if _, err := w.Spend(2, 1, 4); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("Spend now 回退应拒绝, 实际 %v", err)
	}
	if _, err := w.Refund(999, 1, 4); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("Refund now 回退应优先于消费单不存在, 实际 %v", err)
	}
	if bal := w.Balance(4); bal != 0 {
		t.Fatalf("Balance now 回退应返回0, 实际 %d", bal)
	}
	// 时间线未被回退操作改动：now=5 仍正常。
	if _, err := w.Spend(2, 1, 5); err != nil {
		t.Fatalf("时间线应停在 5, now=5 应可消费: %v", err)
	}
}

// TestRejectionOrder：验证拒绝原因按规定顺序只报第一个。
func TestRejectionOrder(t *testing.T) {
	w := New()
	mustGrant(t, w, 1, 10, 20, 5)
	if _, err := w.Spend(1, 3, 6); err != nil {
		t.Fatal(err)
	}

	// Grant：now 回退 > 数量非正 > 批次 id 重复 > 发放即过期。
	if err := w.Grant(3, 0, 20, 1); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("Grant 拒绝顺序错误(应先报 now 回退): %v", err)
	}
	if err := w.Grant(1, 0, 20, 6); reasonOf(err) != ReasonNonPositiveAmount {
		t.Fatalf("Grant 拒绝顺序错误(应先报数量非正): %v", err)
	}
	if err := w.Grant(1, 5, 6, 6); reasonOf(err) != ReasonDuplicateBatchID {
		t.Fatalf("Grant 拒绝顺序错误(应先报批次重复): %v", err)
	}
	if err := w.Grant(2, 5, 6, 6); reasonOf(err) != ReasonGrantExpired {
		t.Fatalf("应报发放即过期: %v", err)
	}
	t.Log("判定依据: 四组非法 Grant 按「now回退→非正→批次重复→发放即过期」只报第一个原因")

	// Spend：now 回退 > 非正 > 消费单重复 > 余额不足。
	if _, err := w.Spend(1, 0, 1); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("Spend 拒绝顺序错误: %v", err)
	}
	if _, err := w.Spend(1, 0, 6); reasonOf(err) != ReasonNonPositiveAmount {
		t.Fatalf("Spend 拒绝顺序错误: %v", err)
	}
	if _, err := w.Spend(1, 5, 6); reasonOf(err) != ReasonDuplicateSpendID {
		t.Fatalf("Spend 拒绝顺序错误(应先报消费单重复): %v", err)
	}
	if _, err := w.Spend(2, 100000, 6); reasonOf(err) != ReasonInsufficientBalance {
		t.Fatalf("应报余额不足: %v", err)
	}

	// Refund：now 回退 > 非正 > 消费单不存在 > 超量。
	if _, err := w.Refund(2, 0, 1); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("Refund 拒绝顺序错误: %v", err)
	}
	if _, err := w.Refund(2, 0, 6); reasonOf(err) != ReasonNonPositiveAmount {
		t.Fatalf("Refund 拒绝顺序错误: %v", err)
	}
	if _, err := w.Refund(404, 1, 6); reasonOf(err) != ReasonUnknownSpend {
		t.Fatalf("应报消费单不存在: %v", err)
	}
	if _, err := w.Refund(1, 4, 6); reasonOf(err) != ReasonRefundTooLarge {
		t.Fatalf("应报退回超量: %v", err)
	}
	t.Log("判定依据: Spend/Refund 各非法组合均按规定拒绝顺序只报第一个原因")
}

// op 是随机对照测试中的一条操作。
// grant: a=id,b=amount,c=exp；spend/refund: a=id,b=amount；所有操作带 now。
type op struct {
	kind string
	a, b int64
	c    int64
	now  int64
}

func applyToReal(w *Wallet, o op) (reason string, d []DeductItem, r []RefundItem, bal int64, hasBal bool) {
	switch o.kind {
	case "grant":
		return string(reasonOf(w.Grant(o.a, o.b, o.c, o.now))), nil, nil, 0, false
	case "spend":
		items, err := w.Spend(o.a, o.b, o.now)
		return string(reasonOf(err)), items, nil, 0, false
	case "refund":
		items, err := w.Refund(o.a, o.b, o.now)
		return string(reasonOf(err)), nil, items, 0, false
	default:
		return "", nil, nil, w.Balance(o.now), true
	}
}

func applyToNaive(n *naiveWallet, o op) (reason string, d []DeductItem, r []RefundItem, bal int64, hasBal bool) {
	switch o.kind {
	case "grant":
		return string(n.grant(o.a, o.b, o.c, o.now)), nil, nil, 0, false
	case "spend":
		items, why := n.spend(o.a, o.b, o.now)
		return string(why), items, nil, 0, false
	case "refund":
		items, why := n.refund(o.a, o.b, o.now)
		return string(why), nil, items, 0, false
	default:
		v, ok := n.balance(o.now)
		return "", nil, nil, v, ok
	}
}

func joinLogs(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

// TestDifferentialAgainstNaive：随机操作流，双实现逐步对照
// （拒绝原因、扣减/退回明细、余额、过期丢弃、批次恒等式）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const iterations = 60
	const opsPerIter = 400
	rng := rand.New(rand.NewSource(20261001))

	for iter := 0; iter < iterations; iter++ {
		w := New()
		n := newNaive()
		var now int64 = 1
		var nextBatchID int64 = 1000
		var nextSpendID int64 = 5000
		knownBatch := []int64{}
		knownSpend := []int64{}
		logs := []string{fmt.Sprintf("--- 随机流 iter=%d ---", iter)}

		for step := 0; step < opsPerIter; step++ {
			if rng.Intn(3) == 0 {
				now += int64(rng.Intn(4))
			}
			var o op
			switch rng.Intn(4) {
			case 0:
				id := nextBatchID
				nextBatchID += 1 + int64(rng.Intn(3))
				if rng.Intn(5) == 0 && len(knownBatch) > 0 {
					id = knownBatch[rng.Intn(len(knownBatch))] // 故意重复 id
				}
				amount := int64(rng.Intn(10)) - 1   // 含 0、负数
				exp := now + int64(rng.Intn(7)) - 1 // 含 exp<=now
				o = op{kind: "grant", a: id, b: amount, c: exp, now: now}
				knownBatch = append(knownBatch, id)
			case 1:
				sid := nextSpendID
				nextSpendID += 1 + int64(rng.Intn(3))
				if rng.Intn(5) == 0 && len(knownSpend) > 0 {
					sid = knownSpend[rng.Intn(len(knownSpend))] // 故意重复 sid
				}
				o = op{kind: "spend", a: sid, b: int64(rng.Intn(12)) - 1, now: now}
				knownSpend = append(knownSpend, sid)
			case 2:
				sid := int64(900000 + rng.Intn(100))
				if len(knownSpend) > 0 && rng.Intn(4) != 0 {
					sid = knownSpend[rng.Intn(len(knownSpend))]
				}
				o = op{kind: "refund", a: sid, b: int64(rng.Intn(12)) - 1, now: now}
			default:
				bnow := now
				if rng.Intn(6) == 0 {
					bnow = now - int64(1+rng.Intn(3)) // 故意回退
				}
				o = op{kind: "balance", now: bnow}
			}

			// Balance 回退被拒时同样返回 0，是否接受需在调用前依据时间线判定。
			realAcceptable := o.kind != "balance" || o.now >= w.lastNow
			naiveAcceptable := o.kind != "balance" || o.now >= n.lastNow
			rErr, rD, rR, rBal, _ := applyToReal(w, o)
			nErr, nD, nR, nBal, nOK := applyToNaive(n, o)
			rOK := realAcceptable
			logs = append(logs, fmt.Sprintf(
				"step=%d %s(a=%d,b=%d,c=%d,now=%d) -> real(err=%q,d=%v,r=%v,bal=%d,ok=%v) naive(err=%q,d=%v,r=%v,bal=%d,ok=%v)",
				step, o.kind, o.a, o.b, o.c, o.now,
				rErr, rD, rR, rBal, rOK, nErr, nD, nR, nBal, nOK))

			if rErr != nErr {
				t.Fatalf("iter=%d step=%d 拒绝原因不一致 %q vs %q\n%s", iter, step, rErr, nErr, joinLogs(logs))
			}
			if !equalDeducts(rD, nD) {
				t.Fatalf("iter=%d step=%d 扣减明细不一致 %v vs %v\n%s", iter, step, rD, nD, joinLogs(logs))
			}
			if !equalRefunds(rR, nR) {
				t.Fatalf("iter=%d step=%d 退回明细不一致 %v vs %v\n%s", iter, step, rR, nR, joinLogs(logs))
			}
			if o.kind == "balance" && (realAcceptable != naiveAcceptable || rOK != nOK) {
				t.Fatalf("iter=%d step=%d 余额接受性不一致 real=(%d,%v) naive=(%d,%v)\n%s",
					iter, step, rBal, rOK, nBal, nOK, joinLogs(logs))
			}
			if realAcceptable && rBal != nBal {
				t.Fatalf("iter=%d step=%d 余额数值不一致 real=%d naive=%d\n%s",
					iter, step, rBal, nBal, joinLogs(logs))
			}
			if w.Discarded() != n.discarded {
				t.Fatalf("iter=%d step=%d 过期丢弃不一致 %d vs %d\n%s",
					iter, step, w.Discarded(), n.discarded, joinLogs(logs))
			}
			if !n.invariant() {
				t.Fatalf("iter=%d step=%d 朴素模型恒等式破坏\n%s", iter, step, joinLogs(logs))
			}
			for _, bs := range w.Batches(o.now) {
				if bs.Remaining+bs.Deducted-bs.Refunded != bs.Granted {
					t.Fatalf("iter=%d step=%d 批次%d 恒等式破坏 %d+%d-%d!=%d\n%s",
						iter, step, bs.ID, bs.Remaining, bs.Deducted, bs.Refunded, bs.Granted, joinLogs(logs))
				}
			}
		}
		t.Logf("iter=%d: %d 步随机操作双实现完全一致（输入/输出逐步已记录）", iter, opsPerIter)
	}
}

// TestReplayDeterministic：相同操作序列重放得到完全相同的明细与余额。
func TestReplayDeterministic(t *testing.T) {
	ops := []op{
		{kind: "grant", a: 1, b: 10, c: 5, now: 1},
		{kind: "grant", a: 2, b: 10, c: 5, now: 1},
		{kind: "grant", a: 3, b: 10, c: 8, now: 2},
		{kind: "spend", a: 1, b: 15, now: 3},
		{kind: "refund", a: 1, b: 7, now: 5},
		{kind: "balance", now: 5},
		{kind: "refund", a: 1, b: 8, now: 6},
		{kind: "balance", now: 6},
	}
	run := func() []string {
		w := New()
		out := []string{}
		for _, o := range ops {
			err, d, r, bal, hasBal := applyToReal(w, o)
			out = append(out, fmt.Sprintf(
				"%s(a=%d,b=%d,c=%d,now=%d)->err=%q d=%v r=%v bal=%d(%v) discarded=%d",
				o.kind, o.a, o.b, o.c, o.now, err, d, r, bal, hasBal, w.Discarded()))
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		t.Logf("重放对照: %s", first[i])
		if first[i] != second[i] {
			t.Fatalf("重放第 %d 步不一致:\n%s\n%s", i, first[i], second[i])
		}
	}
}

// TestConcurrentLinearizable：并发调用不崩溃、结果守恒；配合 -race 检测数据竞争。
func TestConcurrentLinearizable(t *testing.T) {
	w := New()
	const goroutines = 16
	const perG = 200
	for g := 0; g < goroutines; g++ {
		if err := w.Grant(int64(1000+g), 1000, 100, 1); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < perG; k++ {
				sid := int64(g*perG + k)
				if _, err := w.Spend(sid, 1, 50); err == nil {
					if _, err := w.Refund(sid, 1, 50); err != nil {
						t.Errorf("并发 Refund 失败: %v", err)
						return
					}
				}
				_ = w.Balance(50)
			}
		}(g)
	}
	wg.Wait()

	// 所有成功消费均已全额退回（未过期），有效余额应回到初始发放总量。
	if bal := w.Balance(50); bal != goroutines*1000 {
		t.Fatalf("并发后余额应守恒=%d, 实际 %d", goroutines*1000, bal)
	}
	if d := w.Discarded(); d != 0 {
		t.Fatalf("全部批次未过期, 过期丢弃应=0, 实际 %d", d)
	}
	t.Logf("判定依据: %d goroutine 并发 spend/refund/balance 后余额守恒=%d 且丢弃=0",
		goroutines, w.Balance(50))
}
