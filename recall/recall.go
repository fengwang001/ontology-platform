// Package recall 管理召回合约的到期调度与强制买入清单。
package recall

import "math/big"

// Buyin 是一条强制买入记录，时刻取召回合约的 dl。
type Buyin struct {
	ContractID int64
	Qty        int64
	Price      int64
	Penalty    int64
	At         int64
}

// Item 是调度中的一笔召回合约。
type Item struct {
	Dl int64
	ID int64
}

// Scheduler 按 (dl, 合约号) 维护全部未到期召回及买入清单。
// 合约号在入堆时严格递增，故堆序 (dl,id) 与到期处理顺序一致。
type Scheduler struct {
	items  []Item
	buyins []Buyin
}

// New 创建空调度器。
func New() *Scheduler { return &Scheduler{} }

// Push 登记一笔召回合约。
func (s *Scheduler) Push(dl, id int64) {
	s.items = append(s.items, Item{Dl: dl, ID: id})
	s.up(len(s.items) - 1)
}

func (s *Scheduler) less(i, j int) bool {
	a, b := s.items[i], s.items[j]
	return a.Dl < b.Dl || (a.Dl == b.Dl && a.ID < b.ID)
}

func (s *Scheduler) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !s.less(i, parent) {
			return
		}
		s.items[i], s.items[parent] = s.items[parent], s.items[i]
		i = parent
	}
}

func (s *Scheduler) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && s.less(right, left) {
			smallest = right
		}
		if !s.less(smallest, i) {
			return
		}
		s.items[i], s.items[smallest] = s.items[smallest], s.items[i]
		i = smallest
	}
}

func (s *Scheduler) pop() Item {
	n := len(s.items) - 1
	top := s.items[0]
	s.items[0] = s.items[n]
	s.items = s.items[:n]
	if n > 0 {
		s.down(0, n)
	}
	return top
}

// Due 返回 dl<=now 的召回（按 dl、合约号序），并移出调度。
// 已在总账了结（外部归还）的到期项由调用方过滤；这里仅按时钟弹出。
func (s *Scheduler) Due(now int64) []Item {
	var out []Item
	for len(s.items) > 0 && s.items[0].Dl <= now {
		out = append(out, s.pop())
	}
	return out
}

// RecordBuyin 记入一条强制买入清单。
func (s *Scheduler) RecordBuyin(b Buyin) { s.buyins = append(s.buyins, b) }

// Buyins 返回买入清单副本（按时发生顺序，即 (dl,id) 序）。
func (s *Scheduler) Buyins() []Buyin { return append([]Buyin(nil), s.buyins...) }

// Penalty 计算 ceil(r*p*pen/10000)。
func Penalty(r, p, pen int64) int64 {
	rp := new(big.Int).Mul(big.NewInt(r), big.NewInt(p))
	rp.Mul(rp, big.NewInt(pen))
	rp.Add(rp, big.NewInt(9999))
	rp.Quo(rp, big.NewInt(10000))
	return rp.Int64()
}
