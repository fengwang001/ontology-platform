package recall

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// 差分测试：把同一条操作序列分别喂给生产 System 与独立朴素模型，
// 逐步比对错误码与返回值，并校验库存守恒与有效等级一致。
// 每个序列结束后用相同种子重放，要求结果序列逐位相同。

var diffLocations = []string{Warehouse, "病区药柜-A", "病区药柜-B", "病区药柜-C"}
var diffPatients = []string{"患者-甲", "患者-乙", "患者-丙", "患者-丁"}

type diffGen struct {
	rng      *rand.Rand
	now      int64
	batchSeq int
	recSeq   int
}

func lotName(i int) string { return fmt.Sprintf("B%04d", i) }

func (g *diffGen) pickLoc() string { return diffLocations[g.rng.Intn(len(diffLocations))] }
func (g *diffGen) pickPatient() string {
	return diffPatients[g.rng.Intn(len(diffPatients))]
}
func (g *diffGen) qty() int {
	if g.rng.Intn(10) < 8 {
		return 1 + g.rng.Intn(5)
	}
	return 1 + g.rng.Intn(9)
}

// existingBatch 在朴素模型中随机挑一个已入库批次。
func (g *diffGen) existingBatch(n *naiveModel) (drug, batch string, ok bool) {
	drugs := make([]string, 0, len(n.batches))
	for d, bs := range n.batches {
		if len(bs) > 0 {
			drugs = append(drugs, d)
		}
	}
	if len(drugs) == 0 {
		return "", "", false
	}
	sort.Strings(drugs)
	drug = drugs[g.rng.Intn(len(drugs))]
	ids := make([]string, 0, len(n.batches[drug]))
	for b := range n.batches[drug] {
		ids = append(ids, b)
	}
	sort.Strings(ids)
	return drug, ids[g.rng.Intn(len(ids))], true
}

func (g *diffGen) lotEndpoint() string {
	switch g.rng.Intn(10) {
	case 0:
		return "B0000"
	case 1:
		return "B9999"
	default:
		return lotName(g.rng.Intn(2 * (g.batchSeq + 4)))
	}
}

// next 生成下一条操作；第二个返回值为该操作的 now（可能故意回退）。
func (g *diffGen) next(n *naiveModel) (interface{}, int64) {
	rollback := g.rng.Intn(100) < 5
	now := g.now + int64(g.rng.Intn(5))
	if rollback {
		now = g.now - 1 - int64(g.rng.Intn(3))
	}

	mkInvalid := func(op interface{}) interface{} { return op }
	_ = mkInvalid

	drug := fmt.Sprintf("药品-%d", 1+g.rng.Intn(4))

	if g.rng.Intn(100) < 8 {
		// 故意构造参数非法的操作。
		return g.invalidOp(drug), now
	}

	if d, b, ok := g.existingBatch(n); ok && g.rng.Intn(100) >= 25 {
		drug, _ = d, b
		switch g.rng.Intn(10) {
		case 0, 1, 2: // 调拨
			return TransferReq{Now: now, DrugID: d, BatchID: b,
				From: g.pickLoc(), To: g.pickLoc(), Quantity: g.qty()}, now
		case 3, 4, 5: // 发放
			return DispenseReq{Now: now, DrugID: d, BatchID: b,
				Location: g.pickLoc(), Patient: g.pickPatient(),
				Quantity: g.qty(), Consent: g.rng.Intn(2) == 1}, now
		case 6: // 退药
			return ReturnReq{Now: now, DrugID: d, BatchID: b,
				Patient: g.pickPatient(), Quantity: g.qty()}, now
		case 7: // 查询
			return BatchQueryReq{Now: now, DrugID: d, BatchID: b}, now
		}
	}

	switch g.rng.Intn(10) {
	case 0, 1, 2: // 入库
		g.batchSeq++
		return InboundReq{Now: now, DrugID: drug, BatchID: lotName(g.batchSeq),
			Quantity: g.qty()}, now
	case 3, 4, 5: // 召回登记（区间也覆盖未来批次）
		g.recSeq++
		low, high := g.lotEndpoint(), g.lotEndpoint()
		if low > high {
			low, high = high, low
		}
		issue := now - int64(g.rng.Intn(12))
		return RegisterRecallReq{Now: now, RecallID: fmt.Sprintf("R%03d", g.recSeq),
			DrugID: drug, LotLow: low, LotHigh: high,
			Level: 1 + g.rng.Intn(3), IssueAt: issue}, now
	case 6, 7: // 解除
		ids := make([]string, 0, len(n.recalls))
		for id, r := range n.recalls {
			if r.active {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			g.batchSeq++
			return InboundReq{Now: now, DrugID: drug, BatchID: lotName(g.batchSeq),
				Quantity: g.qty()}, now
		}
		sort.Strings(ids)
		return ReleaseRecallReq{Now: now, RecallID: ids[g.rng.Intn(len(ids))]}, now
	default: // 追回清单
		ids := make([]string, 0, len(n.recalls))
		for id := range n.recalls {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			g.batchSeq++
			return InboundReq{Now: now, DrugID: drug, BatchID: lotName(g.batchSeq),
				Quantity: g.qty()}, now
		}
		return RecoveryReq{Now: now, RecallID: ids[g.rng.Intn(len(ids))]}, now
	}
}

func (g *diffGen) invalidOp(drug string) interface{} {
	switch g.rng.Intn(7) {
	case 0:
		return InboundReq{Now: g.now, DrugID: "", BatchID: "B1", Quantity: 3}
	case 1:
		return InboundReq{Now: g.now, DrugID: drug, BatchID: "B1", Quantity: 0}
	case 2:
		return TransferReq{Now: g.now, DrugID: drug, BatchID: "B1",
			From: Warehouse, To: "病区药柜-A", Quantity: 2_000_000}
	case 3:
		return DispenseReq{Now: g.now, DrugID: drug, BatchID: "B1",
			Location: Warehouse, Patient: "", Quantity: 1}
	case 4:
		return RegisterRecallReq{Now: g.now, RecallID: "RX", DrugID: drug,
			LotLow: "B9", LotHigh: "B1", Level: 1, IssueAt: g.now}
	case 5:
		return RegisterRecallReq{Now: g.now, RecallID: "RX", DrugID: drug,
			LotLow: "B1", LotHigh: "B9", Level: 4, IssueAt: g.now}
	default:
		return RegisterRecallReq{Now: g.now, RecallID: "RX", DrugID: drug,
			LotLow: "B1", LotHigh: "B9", Level: 1, IssueAt: g.now + 1}
	}
}

type appliedResult struct {
	code ErrorCode
	info *BatchInfo
	list *RecoveryResult
}

func applyReal(s *System, op interface{}) appliedResult {
	switch q := op.(type) {
	case InboundReq:
		return appliedResult{code: codeOf(s.Inbound(q))}
	case TransferReq:
		return appliedResult{code: codeOf(s.Transfer(q))}
	case DispenseReq:
		return appliedResult{code: codeOf(s.Dispense(q))}
	case ReturnReq:
		return appliedResult{code: codeOf(s.Return(q))}
	case RegisterRecallReq:
		return appliedResult{code: codeOf(s.RegisterRecall(q))}
	case ReleaseRecallReq:
		return appliedResult{code: codeOf(s.ReleaseRecall(q))}
	case BatchQueryReq:
		info, err := s.QueryBatch(q)
		return appliedResult{code: codeOf(err), info: info}
	case RecoveryReq:
		list, err := s.RecoveryList(q)
		return appliedResult{code: codeOf(err), list: list}
	}
	return appliedResult{code: CodeInvalidParam}
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	if e, ok := AsOpError(err); ok {
		return e.Code
	}
	return -1
}

// runSequence 用给定种子跑一条随机序列；返回逐步结果供重放比对。
func runSequence(t *testing.T, w io.Writer, seed int64, steps int) []appliedResult {
	t.Helper()
	s, n := New(), newNaive()
	g := &diffGen{rng: rand.New(rand.NewSource(seed)), now: 1000}
	got := make([]appliedResult, 0, steps)

	fmt.Fprintf(w, "===== 序列 seed=%d steps=%d =====\n", seed, steps)
	for i := 0; i < steps; i++ {
		op, now := g.next(n)
		nr, why := n.apply(op)
		gr := applyReal(s, op)
		got = append(got, gr)

		fmt.Fprintf(w, "步骤%03d now=%d 输入=%#v\n", i, now, op)
		fmt.Fprintf(w, "   朴素: code=%s 依据=%s\n", codeLabel(nr.errCode), why)
		fmt.Fprintf(w, "   生产: code=%s", codeLabel(gr.code))
		if gr.info != nil {
			fmt.Fprintf(w, " 批次=%s 库存=%v 等级=%d 召回=%v",
				gr.info.BatchID, gr.info.Stock, gr.info.EffectiveLevel, gr.info.ActiveRecallIDs)
		}
		if gr.list != nil {
			fmt.Fprintf(w, " 清单=%v", gr.list.Items)
		}
		fmt.Fprintln(w)

		if nr.errCode != gr.code {
			t.Fatalf("seed=%d step=%d 错误码不一致: 朴素=%s 生产=%s op=%#v",
				seed, i, codeLabel(nr.errCode), codeLabel(gr.code), op)
		}
		if nr.info != nil || gr.info != nil {
			if !batchInfoEqual(nr.info, gr.info) {
				t.Fatalf("seed=%d step=%d 查询不一致: 朴素=%#v 生产=%#v",
					seed, i, nr.info, gr.info)
			}
		}
		if !reflect.DeepEqual(nr.list, gr.list) {
			t.Fatalf("seed=%d step=%d 清单不一致: 朴素=%#v 生产=%#v",
				seed, i, nr.list, gr.list)
		}

		// 被接受的操作才推进生成器时钟；拒绝的操作两边都未改时钟。
		if gr.code == 0 {
			g.now = now
		}
		assertInvariants(t, s, n)
	}
	return got
}

func assertInvariants(t *testing.T, s *System, n *naiveModel) {
	t.Helper()
	for drug, bs := range s.inv.batches {
		for batch, l := range bs {
			stockSum := 0
			for _, q := range l.stock {
				stockSum += q
			}
			heldSum := 0
			heldByPatient := map[string]int{}
			for _, d := range l.dispenses {
				left := d.qty - d.returned
				heldSum += left
				heldByPatient[d.patient] += left
			}
			if stockSum+heldSum != l.total {
				t.Fatalf("守恒破坏 drug=%s batch=%s: 库存=%d 持有=%d 入库=%d",
					drug, batch, stockSum, heldSum, l.total)
			}
			for p, h := range heldByPatient {
				if l.heldQty(p) != h {
					t.Fatalf("患者持有缓存不一致 %s/%s/%s: 缓存=%d 实算=%d",
						drug, batch, p, l.heldQty(p), h)
				}
			}
			lv, ids := s.recs.effective(drug, batch)
			nlv, nids := n.effective(drug, batch)
			if len(ids) == 0 {
				ids = []string{}
			}
			if len(nids) == 0 {
				nids = []string{}
			}
			if lv != nlv || !reflect.DeepEqual(ids, nids) {
				t.Fatalf("有效等级不一致 %s/%s: 生产=(%d,%v) 朴素=(%d,%v)",
					drug, batch, lv, ids, nlv, nids)
			}
		}
	}
}

func batchInfoEqual(a, b *BatchInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(*a, *b)
}

func codeLabel(c ErrorCode) string {
	if c == 0 {
		return "成功"
	}
	return c.Name()
}

func TestRandomDifferential(t *testing.T) {
	tracePath := filepath.Join(os.TempDir(), "recall_diff_trace.log")
	f, err := os.Create(tracePath)
	if err != nil {
		t.Fatalf("创建日志文件失败: %v", err)
	}
	defer f.Close()

	const sequences = 1500
	const steps = 60
	for seed := int64(1); seed <= sequences; seed++ {
		first := runSequence(t, f, seed, steps)
		// 相同种子重放，结果必须逐位相同（可复现性）。
		f2 := io.Discard
		second := runSequence(t, f2, seed, steps)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("seed=%d 重放结果不一致", seed)
		}
	}
	t.Logf("差分测试完成: %d 组序列, 每步输入/输出/判定依据见 %s",
		sequences, tracePath)
}
