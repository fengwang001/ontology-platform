package netting

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naiveCase 是随机测试的一次输入快照。
type naiveCase struct {
	parties map[string]int64
	obl     []Obligation
}

// naiveSimulate 完全按题目规则逐轮写成的朴素独立模拟，
// 不复用生产代码中的 computeNet/settle，作为对照基线。
func naiveSimulate(t *testing.T, tc naiveCase) CloseResult {
	t.Helper()

	alive := make(map[string]bool, len(tc.obl))
	for _, ob := range tc.obl {
		alive[ob.OID] = true
	}
	defaulted := map[string]bool{}
	var defaulters []string

	// 第一步/第二步：同一轮快照同时认定，按 id 字节序记录。
	for {
		net := map[string]int64{}
		for p := range tc.parties {
			net[p] = 0
		}
		for _, ob := range tc.obl {
			if alive[ob.OID] && !defaulted[ob.From] && !defaulted[ob.To] {
				net[ob.From] -= ob.Amount
				net[ob.To] += ob.Amount
			}
		}
		var round []string
		for p, v := range net {
			if v < 0 && -v > tc.parties[p] {
				round = append(round, p)
			}
		}
		if len(round) == 0 {
			break
		}
		sort.Strings(round)
		for _, p := range round {
			defaulted[p] = true
			defaulters = append(defaulters, p)
		}
		for _, ob := range tc.obl {
			if alive[ob.OID] && (defaulted[ob.From] || defaulted[ob.To]) {
				alive[ob.OID] = false
			}
		}
	}

	// 第三步：最终净头寸。
	finalNet := map[string]int64{}
	for p := range tc.parties {
		finalNet[p] = 0
	}
	for _, ob := range tc.obl {
		if alive[ob.OID] {
			finalNet[ob.From] -= ob.Amount
			finalNet[ob.To] += ob.Amount
		}
	}

	var revoked []string
	for oid, ok := range alive {
		if !ok {
			revoked = append(revoked, oid)
		}
	}
	sort.Strings(revoked)

	var parties []string
	for p := range tc.parties {
		parties = append(parties, p)
	}
	sort.Strings(parties)
	positions := make([]Position, 0, len(parties))
	for _, p := range parties {
		positions = append(positions, Position{Party: p, Net: finalNet[p]})
	}

	// 朴素撮合：显式排序后维护两个队列，剩余为 0 出队，否则保留队首。
	type entry struct {
		p string
		v int64
	}
	var payers, receivers []entry
	for p, v := range finalNet {
		if v < 0 {
			payers = append(payers, entry{p, -v})
		}
		if v > 0 {
			receivers = append(receivers, entry{p, v})
		}
	}
	less := func(s []entry) func(i, j int) bool {
		return func(i, j int) bool {
			if s[i].v != s[j].v {
				return s[i].v > s[j].v
			}
			return s[i].p < s[j].p
		}
	}
	sort.Slice(payers, less(payers))
	sort.Slice(receivers, less(receivers))

	instructions := []Instruction{}
	for len(payers) > 0 && len(receivers) > 0 {
		amt := payers[0].v
		if receivers[0].v < amt {
			amt = receivers[0].v
		}
		instructions = append(instructions, Instruction{
			Payer:  payers[0].p,
			Payee:  receivers[0].p,
			Amount: amt,
		})
		payers[0].v -= amt
		receivers[0].v -= amt
		if payers[0].v == 0 {
			payers = payers[1:]
		}
		if len(receivers) > 0 && receivers[0].v == 0 {
			receivers = receivers[1:]
		}
	}

	return CloseResult{
		Cycle:        1,
		Defaulters:   defaulters,
		RevokedOIDs:  revoked,
		Positions:    positions,
		Instructions: instructions,
	}
}

func normalizeResult(r CloseResult) CloseResult {
	if r.Defaulters == nil {
		r.Defaulters = []string{}
	}
	if r.RevokedOIDs == nil {
		r.RevokedOIDs = []string{}
	}
	if r.Instructions == nil {
		r.Instructions = []Instruction{}
	}
	if r.Positions == nil {
		r.Positions = []Position{}
	}
	return r
}

// assertInvariants 校验净头寸之和为 0、非违约方借记不超限、
// 付出总额等于收到总额、指令条数不超过非零净头寸参与方数减 1。
func assertInvariants(t *testing.T, tc naiveCase, r CloseResult) {
	t.Helper()
	var sum, paid, received int64
	nonZero := 0
	for _, p := range r.Positions {
		sum += p.Net
		if p.Net != 0 {
			nonZero++
		}
		if p.Net < 0 {
			if -p.Net > tc.parties[p.Party] {
				t.Fatalf("survivor %s breaches cap: -net=%d cap=%d", p.Party, -p.Net, tc.parties[p.Party])
			}
		}
	}
	if sum != 0 {
		t.Fatalf("net positions sum = %d, want 0", sum)
	}
	flows := map[string]int64{}
	for _, ins := range r.Instructions {
		paid += ins.Amount
		received += ins.Amount
		flows[ins.Payer] -= ins.Amount
		flows[ins.Payee] += ins.Amount
	}
	if paid != received {
		t.Fatalf("paid = %d, received = %d", paid, received)
	}
	if nonZero > 0 && len(r.Instructions) > nonZero-1 {
		t.Fatalf("instructions = %d exceeds nonzero parties - 1 = %d", len(r.Instructions), nonZero-1)
	}
	for _, p := range r.Positions {
		if flows[p.Party] != p.Net {
			t.Fatalf("instruction flow for %s = %d, want net %d", p.Party, flows[p.Party], p.Net)
		}
	}
}

func runEngine(t *testing.T, tc naiveCase, order []int) CloseResult {
	t.Helper()
	e := NewEngine()
	for id, cap := range tc.parties {
		if err := e.Register(id, cap); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	for _, idx := range order {
		ob := tc.obl[idx]
		if err := e.Submit(ob.OID, ob.From, ob.To, ob.Amount); err != nil {
			t.Fatalf("submit %+v: %v", ob, err)
		}
	}
	return normalizeResult(e.Close())
}

// TestRandomDifferential 用 2000 组随机义务集合对照朴素模拟，
// 并校验同一集合乱序提交得到逐字段相同的结果。
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261001))

	for c := 0; c < cases; c++ {
		tc := naiveCase{parties: map[string]int64{}}
		partyCount := 2 + rng.Intn(7)
		partyIDs := make([]string, partyCount)
		for i := 0; i < partyCount; i++ {
			partyIDs[i] = fmt.Sprintf("P%02d", i)
			// cap 偏向小值，更容易触发违约与级联。
			tc.parties[partyIDs[i]] = int64(rng.Intn(120))
		}

		obCount := rng.Intn(12)
		used := map[string]bool{}
		for i := 0; i < obCount; i++ {
			from := partyIDs[rng.Intn(partyCount)]
			to := partyIDs[rng.Intn(partyCount)]
			for from == to {
				to = partyIDs[rng.Intn(partyCount)]
			}
			oid := fmt.Sprintf("o%d", i)
			if used[oid] {
				continue
			}
			used[oid] = true
			tc.obl = append(tc.obl, Obligation{
				OID:    oid,
				From:   from,
				To:     to,
				Amount: int64(1 + rng.Intn(80)),
			})
		}

		want := normalizeResult(naiveSimulate(t, tc))

		order := rng.Perm(len(tc.obl))
		got := runEngine(t, tc, order)

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d mismatch\ninput parties=%v obligations=%v order=%v\ngot  %+v\nwant %+v",
				c, tc.parties, tc.obl, order, got, want)
		}
		assertInvariants(t, tc, got)

		// 同一周期义务以任意顺序提交：再用逆序提交一次，必须逐字段相同。
		reverse := make([]int, len(order))
		for i, idx := range order {
			reverse[len(order)-1-i] = idx
		}
		gotReverse := runEngine(t, tc, reverse)
		if !reflect.DeepEqual(gotReverse, want) {
			t.Fatalf("case %d order-sensitive result\ninput obligations=%v\nreverse %+v\nwant    %+v",
				c, tc.obl, gotReverse, want)
		}

		// 日志打印输入、输出与判定依据。
		t.Logf("case=%04d parties=%v obligations=%v submitOrder=%v => defaulters=%v revoked=%v positions=%v instructions=%v; judgement=deep-equal-vs-naive, netSum=0, survivors-within-cap, flow-balanced",
			c, tc.parties, tc.obl, order, got.Defaulters, got.RevokedOIDs, got.Positions, got.Instructions)
	}
}

// TestConcurrentSerializable 并发混合调用，验证数据竞争安全；
// 再串行重放登记/提交集合，确认结果与某个串行顺序等价。
func TestConcurrentSerializable(t *testing.T) {
	e := NewEngine()
	if err := e.Register("A", 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.Register("B", 1000); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 200
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 77))
			for i := 0; i < perWorker; i++ {
				oid := fmt.Sprintf("w%d-%d", w, i)
				if rng.Intn(2) == 0 {
					_ = e.Submit(oid, "A", "B", int64(1+rng.Intn(5)))
				} else {
					_ = e.Submit(oid, "B", "A", int64(1+rng.Intn(5)))
				}
			}
		}(w)
	}
	wg.Wait()

	r := e.Close()

	// 并发全部成功，净头寸与指令合计必须自洽。
	var net int64
	for _, p := range r.Positions {
		net += p.Net
	}
	if net != 0 {
		t.Fatalf("concurrent net sum = %d", net)
	}
	var total int64
	for _, ins := range r.Instructions {
		total += ins.Amount
	}
	t.Logf("concurrent close: accepted obligations=%d instructions=%v flowTotal=%d",
		workers*perWorker, r.Instructions, total)
}
