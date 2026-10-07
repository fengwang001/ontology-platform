// Package naive 是账册系统的独立朴素模型：不用堆、不用索引，
// 一切判定都通过对原始记录的线性扫描直接按规格重算，
// 用于与 ledger.System 做逐操作差分对照。
// 仅共享 ledger 中的类型定义（错误类别、快照结构），不共享任何逻辑。
package naive

import (
	"sort"

	"ontology/ledger"
)

const (
	minNow       = int64(0)
	maxNow       = int64(1_000_000_000)
	maxQty       = 1_000_000
	settleWindow = int64(24 * 3600)
	maxOpenSlips = 3
)

type batch struct {
	id     string
	qty    int
	expiry int64
	seq    int
}

type slip struct {
	id        string
	dept      string
	applicant string
	drug      string
	lines     []ledger.BatchLine
	qty       int
	issueNow  int64
	status    ledger.SlipStatus
	used      int
	returned  int
	residual  int
	diff      int
}

// Model 是朴素账册。
type Model struct {
	last    int64
	started bool
	auth    map[string][2]int64 // person -> [from, until)
	drugs   map[string][]*batch
	slips   map[string]*slip
	nextSeq int
	// 按药品的流水计数，用于校验不变式。
	flows map[string]*drugFlows
}

type drugFlows struct {
	in       int
	destroy  int
	issued   int
	returned int
}

func New() *Model {
	return &Model{
		auth:  make(map[string][2]int64),
		drugs: make(map[string][]*batch),
		slips: make(map[string]*slip),
		flows: make(map[string]*drugFlows),
	}
}

func (m *Model) flow(drug string) *drugFlows {
	f, ok := m.flows[drug]
	if !ok {
		f = &drugFlows{}
		m.flows[drug] = f
	}
	return f
}

func validMoment(t int64) bool { return t >= minNow && t <= maxNow }

func validQty(q int) bool { return q >= 1 && q <= maxQty }

func validAmount(a int) bool { return a >= 0 && a <= maxQty }

func nonEmpty(ss ...string) bool {
	for _, s := range ss {
		if s == "" {
			return false
		}
	}
	return true
}

func (m *Model) checkClock(now int64) *ledger.OpError {
	if m.started && now < m.last {
		return &ledger.OpError{Kind: ledger.ErrClockRollback, Msg: "时钟回退"}
	}
	return nil
}

// GrantAuth 登记授权窗口 [from, until)。
func (m *Model) GrantAuth(now int64, person string, from, until int64) error {
	if !validMoment(now) || !validMoment(from) || !validMoment(until) || from >= until || !nonEmpty(person) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "授权参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	m.auth[person] = [2]int64{from, until}
	m.advance(now)
	return nil
}

// RevokeAuth 在 now 时刻撤销授权。
func (m *Model) RevokeAuth(now int64, person string) error {
	if !validMoment(now) || !nonEmpty(person) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "撤销参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	w, ok := m.auth[person]
	if !ok {
		return &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "人员无授权记录"}
	}
	if now < w[1] {
		m.auth[person] = [2]int64{w[0], now}
	}
	m.advance(now)
	return nil
}

// Inbound 入库登记。
func (m *Model) Inbound(now int64, drug, batchID string, qty int, expiry int64) error {
	if !validMoment(now) || !validMoment(expiry) || !validQty(qty) || !nonEmpty(drug, batchID) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "入库参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	for _, b := range m.drugs[drug] {
		if b.id == batchID {
			return &ledger.OpError{Kind: ledger.ErrState, Msg: "批号已存在"}
		}
	}
	m.drugs[drug] = append(m.drugs[drug], &batch{id: batchID, qty: qty, expiry: expiry, seq: m.nextSeq})
	m.nextSeq++
	m.flow(drug).in += qty
	m.advance(now)
	return nil
}

func (m *Model) deptLocked(dept string, now int64) bool {
	for _, sl := range m.slips {
		if sl.dept != dept {
			continue
		}
		if sl.status == ledger.SlipDiscrepancy {
			return true
		}
		if sl.status == ledger.SlipOpen && now > sl.issueNow+settleWindow {
			return true
		}
	}
	return false
}

func (m *Model) deptOpenCount(dept string) int {
	n := 0
	for _, sl := range m.slips {
		if sl.dept == dept && sl.status == ledger.SlipOpen {
			n++
		}
	}
	return n
}

// Withdraw 领用：线性扫描可发批次，按 (效期, 入库先后) 排序后分出。
func (m *Model) Withdraw(now int64, slipID, dept, applicant, drug string, qty int, reviewers []string) ([]ledger.BatchLine, error) {
	if !validMoment(now) || !validQty(qty) || !nonEmpty(slipID, dept, applicant, drug) || !nonEmpty(reviewers...) {
		return nil, &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "领用参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return nil, e
	}
	if e := checkReviewers(reviewers, applicant, true); e != nil {
		return nil, e
	}
	for _, r := range reviewers {
		if !m.authValid(r, now) {
			return nil, &ledger.OpError{Kind: ledger.ErrAuth, Msg: "复核人授权无效"}
		}
	}
	batches, ok := m.drugs[drug]
	if !ok {
		return nil, &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "药品不存在"}
	}
	if _, dup := m.slips[slipID]; dup {
		return nil, &ledger.OpError{Kind: ledger.ErrState, Msg: "单据已存在"}
	}
	if m.deptLocked(dept, now) {
		return nil, &ledger.OpError{Kind: ledger.ErrDeptLocked, Msg: "科室被锁定"}
	}
	if m.deptOpenCount(dept) >= maxOpenSlips {
		return nil, &ledger.OpError{Kind: ledger.ErrSlipLimit, Msg: "超过未结清单据上限"}
	}
	avail := make([]*batch, 0, len(batches))
	total := 0
	for _, b := range batches {
		if b.qty > 0 && b.expiry > now {
			avail = append(avail, b)
			total += b.qty
		}
	}
	if total < qty {
		return nil, &ledger.OpError{Kind: ledger.ErrStock, Msg: "库存不足"}
	}
	sort.Slice(avail, func(i, j int) bool {
		if avail[i].expiry != avail[j].expiry {
			return avail[i].expiry < avail[j].expiry
		}
		return avail[i].seq < avail[j].seq
	})
	lines := make([]ledger.BatchLine, 0, len(avail))
	remaining := qty
	for _, b := range avail {
		if remaining == 0 {
			break
		}
		take := b.qty
		if take > remaining {
			take = remaining
		}
		b.qty -= take
		remaining -= take
		lines = append(lines, ledger.BatchLine{BatchID: b.id, Qty: take})
	}
	m.slips[slipID] = &slip{
		id: slipID, dept: dept, applicant: applicant, drug: drug,
		lines: lines, qty: qty, issueNow: now, status: ledger.SlipOpen,
	}
	m.flow(drug).issued += qty
	m.advance(now)
	return lines, nil
}

// Settle 结清。
func (m *Model) Settle(now int64, slipID string, used, returned, residual int) error {
	if !validMoment(now) || !validAmount(used) || !validAmount(returned) || !validAmount(residual) || !nonEmpty(slipID) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "结清参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	sl, ok := m.slips[slipID]
	if !ok {
		return &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "单据不存在"}
	}
	if sl.status != ledger.SlipOpen {
		return &ledger.OpError{Kind: ledger.ErrState, Msg: "单据状态不符"}
	}
	sum := used + returned + residual
	if sum > sl.qty {
		return &ledger.OpError{Kind: ledger.ErrQuantity, Msg: "结清合计超出领出量"}
	}
	rest := returned
	for _, ln := range sl.lines {
		if rest == 0 {
			break
		}
		back := ln.Qty
		if back > rest {
			back = rest
		}
		for _, b := range m.drugs[sl.drug] {
			if b.id == ln.BatchID {
				b.qty += back
				break
			}
		}
		rest -= back
	}
	sl.used, sl.returned, sl.residual = used, returned, residual
	if sum == sl.qty {
		sl.status = ledger.SlipSettled
	} else {
		sl.status = ledger.SlipDiscrepancy
		sl.diff = sl.qty - sum
	}
	m.flow(sl.drug).returned += returned
	m.advance(now)
	return nil
}

// ResolveDiscrepancy 差额处理。
func (m *Model) ResolveDiscrepancy(now int64, slipID string, reviewers []string) error {
	if !validMoment(now) || !nonEmpty(slipID) || !nonEmpty(reviewers...) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "差额处理参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	if e := checkReviewers(reviewers, "", false); e != nil {
		return e
	}
	for _, r := range reviewers {
		if !m.authValid(r, now) {
			return &ledger.OpError{Kind: ledger.ErrAuth, Msg: "复核人授权无效"}
		}
	}
	sl, ok := m.slips[slipID]
	if !ok {
		return &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "单据不存在"}
	}
	if sl.status != ledger.SlipDiscrepancy {
		return &ledger.OpError{Kind: ledger.ErrState, Msg: "单据非差额待处理"}
	}
	sl.status = ledger.SlipSettled
	m.advance(now)
	return nil
}

// Destroy 销毁批次账面余量。
func (m *Model) Destroy(now int64, drug, batchID string, qty int, reviewers []string) error {
	if !validMoment(now) || !validQty(qty) || !nonEmpty(drug, batchID) || !nonEmpty(reviewers...) {
		return &ledger.OpError{Kind: ledger.ErrInvalidParam, Msg: "销毁参数非法"}
	}
	if e := m.checkClock(now); e != nil {
		return e
	}
	if e := checkReviewers(reviewers, "", false); e != nil {
		return e
	}
	for _, r := range reviewers {
		if !m.authValid(r, now) {
			return &ledger.OpError{Kind: ledger.ErrAuth, Msg: "复核人授权无效"}
		}
	}
	batches, ok := m.drugs[drug]
	if !ok {
		return &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "药品不存在"}
	}
	var target *batch
	for _, b := range batches {
		if b.id == batchID {
			target = b
			break
		}
	}
	if target == nil {
		return &ledger.OpError{Kind: ledger.ErrNotFound, Msg: "批号不存在"}
	}
	if qty > target.qty {
		return &ledger.OpError{Kind: ledger.ErrQuantity, Msg: "销毁数量超出账面余量"}
	}
	target.qty -= qty
	m.flow(drug).destroy += qty
	m.advance(now)
	return nil
}

// Snapshot 导出 now 时刻的可观测状态，结构与 ledger.System.Snapshot 一致。
func (m *Model) Snapshot(now int64) ledger.Snapshot {
	snap := ledger.Snapshot{
		Now:   now,
		Drugs: make(map[string]ledger.DrugSnapshot, len(m.drugs)),
		Slips: make(map[string]ledger.SlipSnapshot, len(m.slips)),
		Depts: make(map[string]ledger.DeptSnapshot),
	}
	for name, batches := range m.drugs {
		ds := ledger.DrugSnapshot{Batches: make(map[string]int, len(batches))}
		for _, b := range batches {
			ds.Batches[b.id] = b.qty
			ds.BookTotal += b.qty
			if b.expiry > now {
				ds.Available += b.qty
			}
		}
		if f, ok := m.flows[name]; ok {
			ds.TotalIn = f.in
			ds.TotalDestroyed = f.destroy
			ds.TotalIssued = f.issued
			ds.TotalReturned = f.returned
		}
		snap.Drugs[name] = ds
	}
	depts := map[string]bool{}
	for id, sl := range m.slips {
		lines := make([]ledger.BatchLine, len(sl.lines))
		copy(lines, sl.lines)
		snap.Slips[id] = ledger.SlipSnapshot{
			Dept: sl.dept, Applicant: sl.applicant, Drug: sl.drug,
			Lines: lines, Qty: sl.qty, IssueNow: sl.issueNow,
			Deadline: sl.issueNow + settleWindow,
			Status:   sl.status, Used: sl.used, Returned: sl.returned,
			Residual: sl.residual, Diff: sl.diff,
		}
		depts[sl.dept] = true
	}
	for dept := range depts {
		open, disc, total := 0, 0, 0
		for _, sl := range m.slips {
			if sl.dept != dept {
				continue
			}
			total++
			switch sl.status {
			case ledger.SlipOpen:
				open++
			case ledger.SlipDiscrepancy:
				disc++
			}
		}
		snap.Depts[dept] = ledger.DeptSnapshot{
			Locked:      m.deptLocked(dept, now),
			OpenCount:   open,
			Discrepancy: disc,
			TotalSlips:  total,
		}
	}
	return snap
}

func (m *Model) advance(now int64) {
	m.last = now
	m.started = true
}

func (m *Model) authValid(person string, t int64) bool {
	w, ok := m.auth[person]
	return ok && w[0] <= t && t < w[1]
}

func checkReviewers(reviewers []string, applicant string, hasApplicant bool) *ledger.OpError {
	if len(reviewers) != 2 {
		return &ledger.OpError{Kind: ledger.ErrReviewer, Msg: "复核人人数不足"}
	}
	if reviewers[0] == reviewers[1] {
		return &ledger.OpError{Kind: ledger.ErrReviewer, Msg: "两名复核人为同一人"}
	}
	if hasApplicant && (reviewers[0] == applicant || reviewers[1] == applicant) {
		return &ledger.OpError{Kind: ledger.ErrReviewer, Msg: "复核人不得是申请人本人"}
	}
	return nil
}
