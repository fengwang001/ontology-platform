package service_test

// 本文件包含一个独立编写的朴素模型，用于与正式实现做随机操作
// 序列对照。朴素模型刻意使用最直白的写法：切片全量扫描、保留
// 全部历史记录、不做任何懒删除，以此交叉验证正式实现的
// 堆+滚动和优化不改变可观测行为。

import (
	"fmt"
	"sort"
	"testing"

	"ontology/atp/order"
	"ontology/atp/reject"
)

type nInbound struct {
	id        string
	arrival   int64
	qty       int64
	confirmed bool
}

type nResv struct {
	wh     string
	sku    string
	qty    int64
	expiry int64
	state  int // 0=active 1=consumed 2=released
}

type nOrder struct {
	id     string
	expiry int64
	res    []*nResv
}

type nStock struct {
	onHand   int64
	inbounds []*nInbound
	resvs    []*nResv
}

type nWh struct {
	id     string
	prio   int
	stocks map[string]*nStock
}

type naive struct {
	now    int64
	whs    map[string]*nWh
	orders map[string]*nOrder
}

func newNaive() *naive {
	return &naive{whs: map[string]*nWh{}, orders: map[string]*nOrder{}}
}

func (m *naive) stock(wh, sku string, create bool) *nStock {
	w := m.whs[wh]
	if w == nil {
		return nil
	}
	s := w.stocks[sku]
	if s == nil && create {
		s = &nStock{}
		w.stocks[sku] = s
	}
	return s
}

func (m *naive) sortedWhs() []*nWh {
	var list []*nWh
	for _, w := range m.whs {
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].prio != list[j].prio {
			return list[i].prio < list[j].prio
		}
		return list[i].id < list[j].id
	})
	return list
}

func (m *naive) addWarehouse(now int64, id string, prio int) error {
	if now < 0 {
		return reject.New(reject.InvalidParam, -1, "negative time")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	if _, ok := m.whs[id]; ok {
		return reject.New(reject.InvalidParam, -1, "dup warehouse")
	}
	m.whs[id] = &nWh{id: id, prio: prio, stocks: map[string]*nStock{}}
	m.now = now
	return nil
}

func (m *naive) addStock(now int64, wh, sku string, qty int64) error {
	if now < 0 || qty <= 0 || sku == "" {
		return reject.New(reject.InvalidParam, -1, "bad param")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	s := m.stock(wh, sku, false)
	if s == nil {
		if m.whs[wh] == nil {
			return reject.New(reject.InvalidParam, -1, "unknown warehouse")
		}
		s = m.stock(wh, sku, true)
	}
	s.onHand += qty
	m.now = now
	return nil
}

func (m *naive) scheduleInbound(now int64, wh, sku, id string, arrival, qty int64) error {
	if now < 0 || qty <= 0 || sku == "" || id == "" || arrival < 0 {
		return reject.New(reject.InvalidParam, -1, "bad param")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	if m.whs[wh] == nil {
		return reject.New(reject.InvalidParam, -1, "unknown warehouse")
	}
	s := m.stock(wh, sku, true)
	for _, in := range s.inbounds {
		if !in.confirmed && in.id == id {
			return reject.New(reject.InvalidParam, -1, "dup inbound")
		}
	}
	s.inbounds = append(s.inbounds, &nInbound{id: id, arrival: arrival, qty: qty})
	m.now = now
	return nil
}

func (m *naive) confirmInbound(now int64, wh, sku, id string) error {
	if now < 0 || id == "" {
		return reject.New(reject.InvalidParam, -1, "bad param")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	s := m.stock(wh, sku, false)
	if s != nil {
		for _, in := range s.inbounds {
			if in.id == id && !in.confirmed {
				if in.arrival > now {
					return reject.New(reject.InvalidParam, -1, "not arrived")
				}
				in.confirmed = true
				s.onHand += in.qty
				m.now = now
				return nil
			}
		}
	}
	return reject.New(reject.InvalidParam, -1, "unknown inbound")
}

// nAvail 计算可承诺量：现货 + 到货时刻不晚于 cutoff 的未确认入库
// - 在 resNow 仍有效的预留（全量扫描，保留全部历史记录）。
func (m *naive) nAvail(wh, sku string, cutoff, resNow int64) int64 {
	s := m.stock(wh, sku, false)
	if s == nil {
		return 0
	}
	v := s.onHand
	for _, in := range s.inbounds {
		if !in.confirmed && in.arrival <= cutoff {
			v += in.qty
		}
	}
	for _, r := range s.resvs {
		if r.state == 0 && resNow < r.expiry {
			v -= r.qty
		}
	}
	return v
}

func (m *naive) totalSupply(sku string) int64 {
	var total int64
	for _, w := range m.whs {
		if s := w.stocks[sku]; s != nil {
			total += s.onHand
			for _, in := range s.inbounds {
				if !in.confirmed {
					total += in.qty
				}
			}
		}
	}
	return total
}

type nAlloc struct {
	line int
	wh   string
	sku  string
	qty  int64
}

func (m *naive) commit(req order.Request) ([]nAlloc, int64, error) {
	if err := order.Validate(&req); err != nil {
		return nil, 0, err
	}
	now := int64(req.Now)
	if now < m.now {
		return nil, 0, reject.New(reject.ClockRollback, -1, "rollback")
	}
	if o, ok := m.orders[req.OrderID]; ok && now < o.expiry {
		return nil, 0, reject.New(reject.DuplicateOrder, -1, "dup order")
	}
	type key struct{ wh, sku string }
	avail := map[key]int64{}
	get := func(wh, sku string) int64 {
		k := key{wh, sku}
		if v, ok := avail[k]; ok {
			return v
		}
		v := m.nAvail(wh, sku, now, now)
		avail[k] = v
		return v
	}
	var allocs []nAlloc
	used := map[string]bool{}
	allocated := map[string]int64{}
	var perm, temp []int
	for i, ln := range req.Lines {
		need := ln.Qty
		if !req.AllowSplit {
			chosen := ""
			for _, w := range m.sortedWhs() {
				if get(w.id, ln.SKU) >= need {
					chosen = w.id
					break
				}
			}
			if chosen == "" {
				if m.totalSupply(ln.SKU)-allocated[ln.SKU] < ln.Qty {
					perm = append(perm, i)
				} else {
					temp = append(temp, i)
				}
				continue
			}
			avail[key{chosen, ln.SKU}] -= need
			allocs = append(allocs, nAlloc{i, chosen, ln.SKU, need})
			used[chosen] = true
			allocated[ln.SKU] += need
			continue
		}
		for _, w := range m.sortedWhs() {
			if need == 0 {
				break
			}
			a := get(w.id, ln.SKU)
			if a <= 0 {
				continue
			}
			take := a
			if take > need {
				take = need
			}
			avail[key{w.id, ln.SKU}] -= take
			need -= take
			allocs = append(allocs, nAlloc{i, w.id, ln.SKU, take})
			used[w.id] = true
			allocated[ln.SKU] += take
		}
		if need > 0 {
			if m.totalSupply(ln.SKU)-allocated[ln.SKU] < ln.Qty {
				perm = append(perm, i)
			} else {
				temp = append(temp, i)
			}
		}
	}
	if len(perm) > 0 {
		return nil, 0, reject.New(reject.PermanentStockout, perm[0], "perm")
	}
	if len(temp) > 0 {
		return nil, 0, reject.New(reject.TemporaryStockout, temp[0], "temp")
	}
	if len(used) > req.MaxWarehouses {
		return nil, 0, reject.New(reject.TooManySplits, -1, "splits")
	}
	expiry := now + req.TTL
	o := &nOrder{id: req.OrderID, expiry: expiry}
	for _, a := range allocs {
		r := &nResv{wh: a.wh, sku: a.sku, qty: a.qty, expiry: expiry}
		o.res = append(o.res, r)
		m.stock(a.wh, a.sku, true).resvs = append(m.stock(a.wh, a.sku, true).resvs, r)
	}
	m.orders[req.OrderID] = o
	m.now = now
	return allocs, expiry, nil
}

func (m *naive) confirmOutbound(now int64, id string) error {
	if now < 0 || id == "" {
		return reject.New(reject.InvalidParam, -1, "bad param")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	o, ok := m.orders[id]
	if !ok {
		return reject.New(reject.OrderNotFound, -1, "no order")
	}
	if now >= o.expiry {
		return reject.New(reject.ReservationExpired, -1, "expired")
	}
	for _, r := range o.res {
		s := m.stock(r.wh, r.sku, false)
		// 与正式实现一致：先把已到货未确认的入库转为现货。
		for _, in := range s.inbounds {
			if !in.confirmed && in.arrival <= now {
				in.confirmed = true
				s.onHand += in.qty
			}
		}
		s.onHand -= r.qty
		if s.onHand < 0 {
			panic(fmt.Sprintf("naive: negative on-hand at %s/%s", r.wh, r.sku))
		}
		r.state = 1
	}
	delete(m.orders, id)
	m.now = now
	return nil
}

func (m *naive) release(now int64, id string) error {
	if now < 0 || id == "" {
		return reject.New(reject.InvalidParam, -1, "bad param")
	}
	if now < m.now {
		return reject.New(reject.ClockRollback, -1, "rollback")
	}
	o, ok := m.orders[id]
	if !ok || now >= o.expiry {
		return reject.New(reject.OrderNotFound, -1, "no order")
	}
	for _, r := range o.res {
		r.state = 2
	}
	delete(m.orders, id)
	m.now = now
	return nil
}

func (m *naive) queryATP(wh, sku string, at int64) (int64, error) {
	if at < m.now {
		return 0, reject.New(reject.InvalidParam, -1, "query before now")
	}
	if m.whs[wh] == nil {
		return 0, reject.New(reject.InvalidParam, -1, "unknown warehouse")
	}
	return m.nAvail(wh, sku, at, m.now), nil
}

type nOrderDetail struct {
	valid  bool
	expiry int64
	lines  []nAlloc
}

func (m *naive) queryOrder(id string) (*nOrderDetail, error) {
	o, ok := m.orders[id]
	if !ok {
		return nil, reject.New(reject.OrderNotFound, -1, "no order")
	}
	d := &nOrderDetail{valid: m.now < o.expiry, expiry: o.expiry}
	for _, r := range o.res {
		d.lines = append(d.lines, nAlloc{wh: r.wh, sku: r.sku, qty: r.qty})
	}
	return d, nil
}

var _ = testing.T{}
