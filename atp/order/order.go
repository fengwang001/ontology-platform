// Package order 实现订单承诺引擎：在现货、计划入库、有效预留与
// 仓库优先序的共同作用下，为订单行选择发货仓，必要时拆分，
// 并区分永久缺货与暂时缺货。
//
// 承诺为整单全有或全无：引擎先在可承诺量的快照上模拟分配，
// 全部行满足且仓库数不超限后才落账（建立预留）。
package order

import (
	"fmt"

	"ontology/atp/clock"
	"ontology/atp/inventory"
	"ontology/atp/reject"
)

// Line 为一条订单行。
type Line struct {
	SKU string
	Qty int64
}

// Request 为一次订单承诺请求。
type Request struct {
	OrderID string
	Lines   []Line
	// Now 为承诺时刻，不得早于当前时刻。
	Now clock.Time
	// AllowSplit 为是否允许单行拆分多仓。
	AllowSplit bool
	// TTL 为预留时长（秒），到期时刻 = 承诺时刻 + TTL。
	TTL int64
	// MaxWarehouses 为整张订单涉及的不同仓库数上限。
	MaxWarehouses int
}

// Allocation 为一段分配结果：某订单行从某仓取的数量。
type Allocation struct {
	Line      int
	Warehouse string
	SKU       string
	Qty       int64
}

// Result 为承诺成功的结果。
type Result struct {
	OrderID string
	Expiry  clock.Time
	// Allocations 按订单行顺序给出每段的仓库与数量（判定依据）。
	Allocations []Allocation
}

// Validate 校验请求参数合法性（不涉及时钟与库存状态）。
func Validate(req *Request) error {
	if req.Now < 0 {
		return reject.New(reject.InvalidParam, -1, fmt.Sprintf("negative commit time %d", req.Now))
	}
	if req.OrderID == "" {
		return reject.New(reject.InvalidParam, -1, "empty order id")
	}
	if len(req.Lines) == 0 {
		return reject.New(reject.InvalidParam, -1, "order has no lines")
	}
	for i, ln := range req.Lines {
		if ln.SKU == "" {
			return reject.New(reject.InvalidParam, -1, fmt.Sprintf("line %d: empty sku", i))
		}
		if ln.Qty <= 0 {
			return reject.New(reject.InvalidParam, -1, fmt.Sprintf("line %d: non-positive qty %d", i, ln.Qty))
		}
	}
	if req.TTL < 0 {
		return reject.New(reject.InvalidParam, -1, fmt.Sprintf("negative reservation ttl %d", req.TTL))
	}
	if req.MaxWarehouses < 1 {
		return reject.New(reject.InvalidParam, -1, fmt.Sprintf("max warehouses %d < 1", req.MaxWarehouses))
	}
	return nil
}

// Committer 为承诺引擎。无内部状态，可复用。
type Committer struct{}

type whSKU struct {
	wh  string
	sku string
}

// Commit 在 inv 上执行承诺。假定 req 已通过 Validate。
// 拒绝时不改变任何库存状态（懒过期清理除外，其不可观测）。
func (c *Committer) Commit(inv *inventory.Inventory, req *Request) (*Result, error) {
	// 订单重复：同一订单号已存在仍有效的预留。
	if inv.HasActiveOrder(req.OrderID, req.Now) {
		return nil, reject.New(reject.DuplicateOrder, -1, "order already has active reservation: "+req.OrderID)
	}

	avail := make(map[whSKU]int64)
	getAvail := func(wh, sku string) int64 {
		k := whSKU{wh, sku}
		if v, ok := avail[k]; ok {
			return v
		}
		v := inv.Available(wh, sku, req.Now, req.Now)
		avail[k] = v
		return v
	}

	var allocs []Allocation
	usedWh := make(map[string]bool)
	allocated := make(map[string]int64) // 本订单前面行已占用（按商品）
	var permLines, tempLines []int

	classify := func(idx int, ln Line) {
		// 永久缺货：总供给（忽略预留与到货时刻）扣除本订单
		// 前面行已占用后仍不足；否则为暂时缺货。
		remaining := inv.TotalSupply(ln.SKU) - allocated[ln.SKU]
		if remaining < ln.Qty {
			permLines = append(permLines, idx)
		} else {
			tempLines = append(tempLines, idx)
		}
	}

	for i, ln := range req.Lines {
		need := ln.Qty
		if !req.AllowSplit {
			chosen := ""
			for _, w := range inv.Sorted() {
				if getAvail(w.ID, ln.SKU) >= need {
					chosen = w.ID
					break
				}
			}
			if chosen == "" {
				classify(i, ln)
				continue
			}
			avail[whSKU{chosen, ln.SKU}] -= need
			allocs = append(allocs, Allocation{Line: i, Warehouse: chosen, SKU: ln.SKU, Qty: need})
			usedWh[chosen] = true
			allocated[ln.SKU] += need
			continue
		}
		// 允许拆分：按仓库优先序号由小到大依次尽量取用。
		for _, w := range inv.Sorted() {
			if need == 0 {
				break
			}
			a := getAvail(w.ID, ln.SKU)
			if a <= 0 {
				continue
			}
			take := a
			if take > need {
				take = need
			}
			avail[whSKU{w.ID, ln.SKU}] -= take
			need -= take
			allocs = append(allocs, Allocation{Line: i, Warehouse: w.ID, SKU: ln.SKU, Qty: take})
			usedWh[w.ID] = true
			allocated[ln.SKU] += take
		}
		if need > 0 {
			classify(i, ln)
		}
	}

	// 缺货拒绝：永久缺货优先于暂时缺货；同类报下标最小行。
	if len(permLines) > 0 {
		return nil, reject.New(reject.PermanentStockout, permLines[0],
			fmt.Sprintf("line %d (%s x%d): total supply insufficient", permLines[0], req.Lines[permLines[0]].SKU, req.Lines[permLines[0]].Qty))
	}
	if len(tempLines) > 0 {
		return nil, reject.New(reject.TemporaryStockout, tempLines[0],
			fmt.Sprintf("line %d (%s x%d): blocked by reservations or arrival times", tempLines[0], req.Lines[tempLines[0]].SKU, req.Lines[tempLines[0]].Qty))
	}

	// 拆分仓库数上限：整单涉及的不同仓库数超限即拒绝，
	// 不为凑够上限改用其他取用方案。
	if len(usedWh) > req.MaxWarehouses {
		return nil, reject.New(reject.TooManySplits, -1,
			fmt.Sprintf("order involves %d warehouses, limit %d", len(usedWh), req.MaxWarehouses))
	}

	// 落账：各用到的仓库为该订单建立预留。
	expiry := req.Now + clock.Time(req.TTL)
	rec := &inventory.OrderRecord{ID: req.OrderID, Expiry: expiry}
	for _, a := range allocs {
		rec.Res = append(rec.Res, inv.NewReservation(req.OrderID, a.Warehouse, a.SKU, a.Qty, expiry))
	}
	inv.CreateOrder(rec)

	return &Result{OrderID: req.OrderID, Expiry: expiry, Allocations: allocs}, nil
}
