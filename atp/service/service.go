// Package service 是电商多仓库存可承诺量查询与订单承诺系统的
// 统一入口。所有操作可并发调用，内部以互斥锁串行化，结果等价于
// 某个串行顺序；相同操作序列重放得到完全相同的发货仓分配。
//
// 时钟规则：每个变更类操作携带时刻，不得小于上一次被接受操作的
// 时刻，否则报时钟回退；被拒绝的操作不改变任何状态与时钟。
// 查询类操作只读，不推进时钟。
//
// 拒绝优先级：参数非法 > 时钟回退 > 订单重复 > 永久缺货 >
// 暂时缺货 > 拆分过多。
package service

import (
	"sync"

	"ontology/atp/clock"
	"ontology/atp/inventory"
	"ontology/atp/order"
	"ontology/atp/reject"
)

// Service 为系统门面。
type Service struct {
	mu  sync.Mutex
	clk clock.Clock
	inv *inventory.Inventory
	eng order.Committer
}

// New 创建空系统。
func New() *Service {
	return &Service{inv: inventory.New()}
}

// Now 返回当前时刻（上一次被接受操作的时刻）。
func (s *Service) Now() clock.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clk.Now()
}

// Stats 返回可承诺量计算中考察的预留记录总数与懒过期扫描次数，
// 用于验证考察次数不随历史订单总数或已失效预留数增长。
func (s *Service) Stats() (examined, sweeps uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Stats()
}

// CheckInvariants 校验不变量：现货非负、有效预留之和不超过可承诺上限。
func (s *Service) CheckInvariants() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.CheckInvariants(s.clk.Now())
}

// mutate 执行变更类操作的公共骨架：
// 先参数校验，再时钟回退检查，最后执行；全部通过才推进时钟。
// apply 内部若拒绝，不得改变任何状态。
func (s *Service) mutate(now clock.Time, validate func() error, apply func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return reject.New(reject.InvalidParam, -1, "negative time")
	}
	if err := validate(); err != nil {
		return err
	}
	if now < s.clk.Now() {
		return reject.New(reject.ClockRollback, -1, "operation time before last accepted time")
	}
	if err := apply(); err != nil {
		return err
	}
	s.clk.Accept(now)
	return nil
}

// AddWarehouse 新增仓库，priority 小者优先。
func (s *Service) AddWarehouse(now clock.Time, id string, priority int) error {
	return s.mutate(now, func() error {
		if id == "" {
			return reject.New(reject.InvalidParam, -1, "empty warehouse id")
		}
		if priority < 0 {
			return reject.New(reject.InvalidParam, -1, "negative priority")
		}
		return nil
	}, func() error {
		return s.inv.AddWarehouse(id, priority)
	})
}

// AddStock 增加某仓某商品的现货。
func (s *Service) AddStock(now clock.Time, whID, sku string, qty int64) error {
	return s.mutate(now, func() error {
		if sku == "" {
			return reject.New(reject.InvalidParam, -1, "empty sku")
		}
		if qty <= 0 {
			return reject.New(reject.InvalidParam, -1, "non-positive qty")
		}
		return nil
	}, func() error {
		return s.inv.AddStock(whID, sku, qty)
	})
}

// ScheduleInbound 登记一条计划入库（到货时刻、数量）。
func (s *Service) ScheduleInbound(now clock.Time, whID, sku, inboundID string, arrival clock.Time, qty int64) error {
	return s.mutate(now, func() error {
		if sku == "" || inboundID == "" {
			return reject.New(reject.InvalidParam, -1, "empty sku or inbound id")
		}
		if qty <= 0 {
			return reject.New(reject.InvalidParam, -1, "non-positive qty")
		}
		if arrival < 0 {
			return reject.New(reject.InvalidParam, -1, "negative arrival time")
		}
		return nil
	}, func() error {
		return s.inv.ScheduleInbound(whID, sku, &inventory.Inbound{ID: inboundID, Arrival: arrival, Qty: qty})
	})
}

// ConfirmInbound 确认到货：把尚未到货确认的计划入库转为现货。
func (s *Service) ConfirmInbound(now clock.Time, whID, sku, inboundID string) error {
	return s.mutate(now, func() error {
		if inboundID == "" {
			return reject.New(reject.InvalidParam, -1, "empty inbound id")
		}
		return nil
	}, func() error {
		return s.inv.ConfirmInbound(whID, sku, inboundID, now)
	})
}

// Commit 执行订单承诺。成功返回分配结果；失败返回带类别的拒绝错误。
func (s *Service) Commit(req order.Request) (*order.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := order.Validate(&req); err != nil {
		return nil, err
	}
	if req.Now < s.clk.Now() {
		return nil, reject.New(reject.ClockRollback, -1, "commit time before last accepted time")
	}
	res, err := s.eng.Commit(s.inv, &req)
	if err != nil {
		return nil, err
	}
	s.clk.Accept(req.Now)
	return res, nil
}

// ConfirmOutbound 确认出库：把有效预留转为现货扣减。
// 预留已到期报 ReservationExpired；订单不存在报 OrderNotFound。
func (s *Service) ConfirmOutbound(now clock.Time, orderID string) error {
	return s.mutate(now, func() error {
		if orderID == "" {
			return reject.New(reject.InvalidParam, -1, "empty order id")
		}
		return nil
	}, func() error {
		return s.inv.ConfirmOutbound(orderID, now)
	})
}

// Release 释放仍有效的预留。订单不存在或预留已到期均报 OrderNotFound。
func (s *Service) Release(now clock.Time, orderID string) error {
	return s.mutate(now, func() error {
		if orderID == "" {
			return reject.New(reject.InvalidParam, -1, "empty order id")
		}
		return nil
	}, func() error {
		return s.inv.Release(orderID, now)
	})
}

// QueryATP 查询某仓某商品在给定承诺时刻的可承诺量。
// 只读，不推进时钟；预留是否有效以当前时钟判定；
// 查询时刻早于当前时刻报参数非法。
func (s *Service) QueryATP(whID, sku string, at clock.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.clk.Now() {
		return 0, reject.New(reject.InvalidParam, -1, "query time before current time")
	}
	if _, ok := s.inv.Warehouse(whID); !ok {
		return 0, reject.New(reject.InvalidParam, -1, "unknown warehouse: "+whID)
	}
	return s.inv.Available(whID, sku, at, s.clk.Now()), nil
}

// QueryOrder 查询某订单的预留明细。只读，不推进时钟；
// 预留是否有效以当前时钟判定。
func (s *Service) QueryOrder(orderID string) (*inventory.OrderDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.GetOrder(orderID, s.clk.Now())
}
