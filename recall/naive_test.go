package recall

import (
	"fmt"
	"sort"
)

// 本文件实现一个与生产代码完全独立的"朴素模型"：
// 每条判定都从全量数据重新扫描计算，刻意不使用任何索引或增量游标。
// 差分测试把同一条操作分别喂给朴素模型与生产 System，
// 要求错误码与全部可观察结果完全一致。

type naiveDispense struct {
	seq      int
	at       int64
	patient  string
	location string
	qty      int
	returned int
}

type naiveBatch struct {
	drug      string
	batch     string
	total     int
	stock     map[string]int
	dispenses []*naiveDispense
}

type naiveRecall struct {
	id, drug, low, high string
	level               int
	issueAt             int64
	active              bool
}

type naiveModel struct {
	lastNow int64
	batches map[string]map[string]*naiveBatch // drug -> batch
	recalls map[string]*naiveRecall
	seq     int
}

func newNaive() *naiveModel {
	return &naiveModel{
		batches: map[string]map[string]*naiveBatch{},
		recalls: map[string]*naiveRecall{},
	}
}

func (n *naiveModel) has(drug, batch string) bool {
	b, ok := n.batches[drug]
	return ok && b[batch] != nil
}

// effective 全量扫描：遍历所有召回（含已解除的也判断后跳过），
// 仅为对照生产实现的判定正确性，不追求性能。
func (n *naiveModel) effective(drug, batch string) (int, []string) {
	ids := []string{}
	for _, r := range n.recalls {
		if r.active && r.drug == drug && r.low <= batch && batch <= r.high {
			ids = append(ids, r.id)
		}
	}
	sort.Strings(ids)
	level := 0
	best := []string{}
	for _, id := range ids {
		lv := n.recalls[id].level
		switch {
		case level == 0 || lv < level:
			level, best = lv, []string{id}
		case lv == level:
			best = append(best, id)
		}
	}
	return level, best
}

type naiveResult struct {
	errCode ErrorCode // 0 表示成功
	info    *BatchInfo
	list    *RecoveryResult
}

func errResult(c ErrorCode) naiveResult { return naiveResult{errCode: c} }

// apply 返回错误码与可能的查询结果。why 给出判定依据，供日志打印。
func (n *naiveModel) apply(op interface{}) (res naiveResult, why string) {
	switch q := op.(type) {
	case InboundReq:
		if q.DrugID == "" || q.BatchID == "" || q.Quantity < 1 || q.Quantity > maxQty {
			return errResult(CodeInvalidParam), "标识为空或数量越界"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if n.has(q.DrugID, q.BatchID) {
			return errResult(CodeInvalidState), "批号重复入库"
		}
		b := map[string]*naiveBatch{}
		if n.batches[q.DrugID] != nil {
			b = n.batches[q.DrugID]
		}
		n.batches[q.DrugID] = b
		b[q.BatchID] = &naiveBatch{
			drug: q.DrugID, batch: q.BatchID, total: q.Quantity,
			stock: map[string]int{Warehouse: q.Quantity},
		}
		n.lastNow = q.Now
		return naiveResult{}, "入库进入药库"

	case TransferReq:
		if q.DrugID == "" || q.BatchID == "" || q.From == "" || q.To == "" ||
			q.Quantity < 1 || q.Quantity > maxQty {
			return errResult(CodeInvalidParam), "标识为空或数量越界"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if !n.has(q.DrugID, q.BatchID) {
			return errResult(CodeNotFound), "批次不存在"
		}
		lv, _ := n.effective(q.DrugID, q.BatchID)
		if lv == 1 {
			return errResult(CodeRecallForbidden), "一级冻结禁止调拨"
		}
		if lv == 2 && q.To != Warehouse {
			return errResult(CodeRecallForbidden), "二级只准调入药库"
		}
		b := n.batches[q.DrugID][q.BatchID]
		if b.stock[q.From] < q.Quantity {
			return errResult(CodeInsufficientStock), "出发库存不足"
		}
		b.stock[q.From] -= q.Quantity
		b.stock[q.To] += q.Quantity
		n.lastNow = q.Now
		return naiveResult{}, fmt.Sprintf("按等级%d允许的调拨完成", lv)

	case DispenseReq:
		if q.DrugID == "" || q.BatchID == "" || q.Location == "" || q.Patient == "" ||
			q.Quantity < 1 || q.Quantity > maxQty {
			return errResult(CodeInvalidParam), "标识为空或数量越界"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if !n.has(q.DrugID, q.BatchID) {
			return errResult(CodeNotFound), "批次不存在"
		}
		lv, _ := n.effective(q.DrugID, q.BatchID)
		if lv == 1 || lv == 2 {
			return errResult(CodeRecallForbidden), fmt.Sprintf("%d级禁止发放", lv)
		}
		if lv == 3 && !q.Consent {
			return errResult(CodeConsentRequired), "三级缺知情确认"
		}
		b := n.batches[q.DrugID][q.BatchID]
		if b.stock[q.Location] < q.Quantity {
			return errResult(CodeInsufficientStock), "位置库存不足"
		}
		b.stock[q.Location] -= q.Quantity
		n.seq++
		b.dispenses = append(b.dispenses, &naiveDispense{
			seq: n.seq, at: q.Now, patient: q.Patient, location: q.Location, qty: q.Quantity,
		})
		n.lastNow = q.Now
		return naiveResult{}, fmt.Sprintf("按等级%v发放完成", lvDesc(lv, q.Consent))

	case ReturnReq:
		if q.DrugID == "" || q.BatchID == "" || q.Patient == "" ||
			q.Quantity < 1 || q.Quantity > maxQty {
			return errResult(CodeInvalidParam), "标识为空或数量越界"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if !n.has(q.DrugID, q.BatchID) {
			return errResult(CodeNotFound), "批次不存在"
		}
		b := n.batches[q.DrugID][q.BatchID]
		held := 0
		for _, d := range b.dispenses {
			if d.patient == q.Patient {
				held += d.qty - d.returned
			}
		}
		if held < q.Quantity {
			return errResult(CodeReturnExceeded), "退药超过未退回总量"
		}
		// 朴素 FIFO：每次重新按 (at, seq) 全量排序本人记录再抵扣。
		own := []*naiveDispense{}
		for _, d := range b.dispenses {
			if d.patient == q.Patient {
				own = append(own, d)
			}
		}
		sort.Slice(own, func(i, j int) bool {
			if own[i].at != own[j].at {
				return own[i].at < own[j].at
			}
			return own[i].seq < own[j].seq
		})
		rem := q.Quantity
		for _, d := range own {
			take := d.qty - d.returned
			if take > rem {
				take = rem
			}
			d.returned += take
			rem -= take
			if rem == 0 {
				break
			}
		}
		b.stock[Warehouse] += q.Quantity
		n.lastNow = q.Now
		return naiveResult{}, "按FIFO抵扣并回库"

	case RegisterRecallReq:
		if q.RecallID == "" || q.DrugID == "" || q.LotLow == "" || q.LotHigh == "" ||
			q.LotLow > q.LotHigh || q.Level < 1 || q.Level > 3 {
			return errResult(CodeInvalidParam), "召回参数非法"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if q.IssueAt > q.Now {
			return errResult(CodeInvalidParam), "始发时刻晚于now"
		}
		if _, ok := n.recalls[q.RecallID]; ok {
			return errResult(CodeInvalidState), "召回编号重复"
		}
		n.recalls[q.RecallID] = &naiveRecall{
			id: q.RecallID, drug: q.DrugID, low: q.LotLow, high: q.LotHigh,
			level: q.Level, issueAt: q.IssueAt, active: true,
		}
		n.lastNow = q.Now
		return naiveResult{}, "召回登记生效"

	case ReleaseRecallReq:
		if q.RecallID == "" {
			return errResult(CodeInvalidParam), "编号为空"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		r, ok := n.recalls[q.RecallID]
		if !ok {
			return errResult(CodeNotFound), "召回不存在"
		}
		if !r.active {
			return errResult(CodeInvalidState), "召回已解除"
		}
		r.active = false
		n.lastNow = q.Now
		return naiveResult{}, "解除只影响该条"

	case BatchQueryReq:
		if q.DrugID == "" || q.BatchID == "" {
			return errResult(CodeInvalidParam), "标识为空"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		if !n.has(q.DrugID, q.BatchID) {
			return errResult(CodeNotFound), "批次不存在"
		}
		lv, ids := n.effective(q.DrugID, q.BatchID)
		b := n.batches[q.DrugID][q.BatchID]
		locs := []string{}
		for loc := range b.stock {
			locs = append(locs, loc)
		}
		sort.Strings(locs)
		stocks := []StockAt{}
		for _, loc := range locs {
			if b.stock[loc] > 0 {
				stocks = append(stocks, StockAt{Location: loc, Quantity: b.stock[loc]})
			}
		}
		n.lastNow = q.Now
		return naiveResult{info: &BatchInfo{
			DrugID: q.DrugID, BatchID: q.BatchID, Stock: stocks,
			EffectiveLevel: lv, ActiveRecallIDs: ids,
		}}, "全量扫描有效等级"

	case RecoveryReq:
		if q.RecallID == "" {
			return errResult(CodeInvalidParam), "编号为空"
		}
		if q.Now < n.lastNow {
			return errResult(CodeClockRollback), "now<lastNow"
		}
		r, ok := n.recalls[q.RecallID]
		if !ok {
			return errResult(CodeNotFound), "召回不存在"
		}
		if !r.active {
			return errResult(CodeInvalidState), "召回已解除"
		}
		if r.level == 3 {
			return errResult(CodeInvalidState), "三级不可查清单"
		}
		type pb struct{ patient, batch string }
		sum := map[pb]int{}
		// 朴素实现：遍历所有药品的所有批次，再跳过不相关的（刻意全扫）。
		drugs := []string{}
		for d := range n.batches {
			drugs = append(drugs, d)
		}
		sort.Strings(drugs)
		for _, drug := range drugs {
			for _, b := range n.batches[drug] {
				if drug != r.drug || !(r.low <= b.batch && b.batch <= r.high) {
					continue
				}
				for _, d := range b.dispenses {
					if d.at >= r.issueAt {
						if left := d.qty - d.returned; left > 0 {
							sum[pb{d.patient, b.batch}] += left
						}
					}
				}
			}
		}
		keys := []pb{}
		for k, v := range sum {
			if v > 0 {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].batch != keys[j].batch {
				return keys[i].batch < keys[j].batch
			}
			return keys[i].patient < keys[j].patient
		})
		items := []RecoveryItem{}
		for _, k := range keys {
			items = append(items, RecoveryItem{
				Patient: k.patient, BatchID: k.batch, DrugID: r.drug, Qty: sum[k],
			})
		}
		n.lastNow = q.Now
		return naiveResult{list: &RecoveryResult{RecallID: q.RecallID, Items: items}},
			"全量扫描覆盖批次与始发后发放"
	}
	return errResult(CodeInvalidParam), "未知操作"
}

func lvDesc(lv int, consent bool) string {
	if lv == 3 {
		return fmt.Sprintf("3(确认=%v)", consent)
	}
	if lv == 0 {
		return "0(无召回)"
	}
	return fmt.Sprintf("%d", lv)
}
