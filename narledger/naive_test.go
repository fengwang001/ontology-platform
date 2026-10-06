package narledger

import (
	"sort"
)

// naiveLedger 是独立编写的朴素参考模型：
// 不使用任何堆/计数器，所有判定都全量扫描批次与单据列表，
// 与优化实现共享同一套业务规则与错误优先级。
type naiveLedger struct {
	lastNow int64
	grants  map[string][][2]int64 // person -> [start,end) 列表
	revoked map[string]int64      // 朴素模型同样支持撤销：记录一次 at 后重写区间
	batches []*naiveBatch
	orders  map[string]*naiveOrder
}

type naiveBatch struct {
	drug, lot     string
	qty, expireAt int64
	received      int64 // 入库量（销毁后记录仍保留）
}

type naiveOrder struct {
	id, dept, applicant, drug          string
	qty, createdAt, deadline           int64
	status                             string // OPEN/SETTLED/DISCREPANCY
	shortfall, used, returned, residue int64
	allocs                             []allocation
}

func newNaive() *naiveLedger {
	return &naiveLedger{grants: map[string][][2]int64{}, revoked: map[string]int64{}, orders: map[string]*naiveOrder{}}
}

func (n *naiveLedger) authValid(person string, t int64) bool {
	for _, iv := range n.grants[person] {
		if iv[0] <= t && t < iv[1] {
			return true
		}
	}
	return false
}

func (n *naiveLedger) err(code ErrorCode, reason string) *OpError {
	return &OpError{Code: code, Op: "naive", Reason: reason}
}

func (n *naiveLedger) reviewers(r1, r2, applicant string, now int64) *OpError {
	if r1 == "" || r2 == "" {
		return n.err(ErrReviewer, "人数不足")
	}
	if r1 == r2 {
		return n.err(ErrReviewer, "同一人")
	}
	if applicant != "" && (r1 == applicant || r2 == applicant) {
		return n.err(ErrReviewer, "申请人本人")
	}
	if !n.authValid(r1, now) || !n.authValid(r2, now) {
		return n.err(ErrUnauthorized, "授权无效")
	}
	return nil
}

func (n *naiveLedger) clock(now int64) *OpError {
	if now < n.lastNow {
		return n.err(ErrClockRollback, "回退")
	}
	return nil
}

func (n *naiveLedger) grant(person string, start, end int64) error {
	if person == "" || start < 0 || start > 1e9 || end < 0 || end > 1e9 || start >= end {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(start); err != nil {
		return err
	}
	n.grants[person] = append(n.grants[person], [2]int64{start, end})
	n.lastNow = start
	return nil
}

func (n *naiveLedger) revoke(person string, at int64) error {
	if person == "" {
		return n.err(ErrInvalidParam, "参数")
	}
	if at < 0 || at > 1e9 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(at); err != nil {
		return err
	}
	list, ok := n.grants[person]
	if !ok || len(list) == 0 {
		return n.err(ErrNotFound, "无授权")
	}
	var kept [][2]int64
	for _, g := range list {
		if g[1] <= at {
			kept = append(kept, g)
		} else if g[0] < at {
			kept = append(kept, [2]int64{g[0], at})
		}
	}
	if len(kept) == 0 {
		delete(n.grants, person)
	} else {
		n.grants[person] = kept
	}
	n.lastNow = at
	return nil
}

func (n *naiveLedger) receive(now int64, drug, lot string, qty, expireAt int64) error {
	if drug == "" || lot == "" || now < 0 || now > 1e9 ||
		qty < 1 || qty > 1e6 || expireAt < 0 || expireAt > 1e9 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(now); err != nil {
		return err
	}
	for _, b := range n.batches {
		if b.drug == drug && b.lot == lot {
			return n.err(ErrState, "批号重复")
		}
	}
	n.batches = append(n.batches, &naiveBatch{drug: drug, lot: lot, qty: qty, expireAt: expireAt, received: qty})
	n.lastNow = now
	return nil
}

// deptLocked 全量扫描：存在逾期 OPEN 或 DISCREPANCY 即锁定。
func (n *naiveLedger) deptLocked(dept string, now int64) bool {
	for _, o := range n.orders {
		if o.dept != dept {
			continue
		}
		if o.status == "DISCREPANCY" {
			return true
		}
		if o.status == "OPEN" && now > o.deadline {
			return true
		}
	}
	return false
}

func (n *naiveLedger) openCount(dept string) int {
	c := 0
	for _, o := range n.orders {
		if o.dept == dept && o.status == "OPEN" {
			c++
		}
	}
	return c
}

func (n *naiveLedger) dispense(now int64, id, dept, applicant, drug string, qty int64, r1, r2 string) error {
	if id == "" || dept == "" || applicant == "" || drug == "" || r1 == "" || r2 == "" ||
		now < 0 || now > 1e9 || qty < 1 || qty > 1e6 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if err := n.reviewers(r1, r2, applicant, now); err != nil {
		return err
	}
	drugExists := false
	for _, b := range n.batches {
		if b.drug == drug {
			drugExists = true
			break
		}
	}
	if !drugExists {
		return n.err(ErrNotFound, "药品不存在")
	}
	if n.deptLocked(dept, now) {
		return n.err(ErrDeptLocked, "锁定")
	}
	if n.openCount(dept) >= 3 {
		return n.err(ErrOpenLimit, "上限")
	}
	// FEFO：全量收集可发批次并排序（朴素做法）
	var cands []*naiveBatch
	for _, b := range n.batches {
		if b.drug == drug && b.qty > 0 && now < b.expireAt {
			cands = append(cands, b)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].expireAt < cands[j].expireAt })
	var total int64
	for _, b := range cands {
		total += b.qty
	}
	if total < qty {
		return n.err(ErrStock, "不足")
	}
	o := &naiveOrder{
		id: id, dept: dept, applicant: applicant, drug: drug, qty: qty,
		createdAt: now, deadline: now + 86400, status: "OPEN",
	}
	remaining := qty
	for _, b := range cands {
		if remaining == 0 {
			break
		}
		give := b.qty
		if give > remaining {
			give = remaining
		}
		b.qty -= give
		o.allocs = append(o.allocs, allocation{drug: drug, lot: b.lot, qty: give})
		remaining -= give
	}
	n.orders[id] = o
	n.lastNow = now
	return nil
}

func (n *naiveLedger) settle(now int64, id string, used, returned, residue int64) error {
	if id == "" || now < 0 || now > 1e9 ||
		used < 0 || used > 1e6 || returned < 0 || returned > 1e6 || residue < 0 || residue > 1e6 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return n.err(ErrNotFound, "单据不存在")
	}
	if o.status != "OPEN" {
		return n.err(ErrState, "状态")
	}
	total := used + returned + residue
	if total > o.qty {
		return n.err(ErrExceed, "超出")
	}
	rest := returned
	for i := range o.allocs {
		a := &o.allocs[i]
		give := rest
		if give > a.qty {
			give = a.qty
		}
		if give > 0 {
			for _, b := range n.batches {
				if b.drug == a.drug && b.lot == a.lot {
					b.qty += give
					break
				}
			}
			rest -= give
		}
		if rest == 0 {
			break
		}
	}
	o.used, o.returned, o.residue = used, returned, residue
	if total < o.qty {
		o.status = "DISCREPANCY"
		o.shortfall = o.qty - total
	} else {
		o.status = "SETTLED"
	}
	n.lastNow = now
	return nil
}

func (n *naiveLedger) resolve(now int64, id, r1, r2 string) error {
	if id == "" || r1 == "" || r2 == "" || now < 0 || now > 1e9 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if err := n.reviewers(r1, r2, "", now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return n.err(ErrNotFound, "单据不存在")
	}
	if o.status != "DISCREPANCY" {
		return n.err(ErrState, "状态")
	}
	o.status = "SETTLED"
	n.lastNow = now
	return nil
}

func (n *naiveLedger) destroy(now int64, drug, lot string, qty int64, r1, r2 string) error {
	if drug == "" || lot == "" || r1 == "" || r2 == "" ||
		now < 0 || now > 1e9 || qty < 1 || qty > 1e6 {
		return n.err(ErrInvalidParam, "参数")
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if err := n.reviewers(r1, r2, "", now); err != nil {
		return err
	}
	var b *naiveBatch
	for _, cand := range n.batches {
		if cand.drug == drug && cand.lot == lot {
			b = cand
			break
		}
	}
	if b == nil {
		return n.err(ErrNotFound, "批次不存在")
	}
	if qty > b.qty {
		return n.err(ErrExceed, "超出")
	}
	b.qty -= qty
	n.lastNow = now
	return nil
}

func (n *naiveLedger) snapshot() Snapshot {
	s := Snapshot{LastNow: n.lastNow, Batches: map[string]BatchView{}, Orders: map[string]OrderView{}}
	for _, b := range n.batches {
		key := b.drug + "\x00" + b.lot
		s.Batches[key] = BatchView{Drug: b.drug, Lot: b.lot, Balance: b.qty, ExpireAt: b.expireAt}
	}
	for id, o := range n.orders {
		v := OrderView{
			Dept: o.dept, Applicant: o.applicant, Drug: o.drug, Status: o.status,
			Qty: o.qty, Shortfall: o.shortfall, CreatedAt: o.createdAt, Deadline: o.deadline,
			Used: o.used, Returned: o.returned, Residue: o.residue,
		}
		for _, a := range o.allocs {
			v.Lots = append(v.Lots, a.lot+":"+itoa(a.qty))
		}
		s.Orders[id] = v
	}
	return s
}
