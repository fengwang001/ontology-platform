// Package grade 登记收货分级并计算每次收货的实付退款。
package grade

import (
	"errors"
	"sync"

	"ontology/refund"
	"ontology/rma"
)

// ErrInvalid 为 grade 自身参数非法；其余错误透传 rma 哨兵，errors.Is 可区分。
var ErrInvalid = errors.New("grade: invalid argument")

type Grade int

const (
	A Grade = 1
	B Grade = 2
	C Grade = 3
)

func (g Grade) valid() bool { return g == A || g == B || g == C }

// Result 为一次收货的钱：Due 应退、Deducted 手续费、Paid 实付。
type Result struct {
	Due      int64
	Deducted int64
	Paid     int64
}

type lineKey struct {
	order rma.OrderID
	line  rma.LineID
}

type counts struct {
	a int64
	b int64
}

type Service struct {
	r    *rma.RMA
	calc *refund.Calculator
	fee  int64

	mu  sync.Mutex
	got map[lineKey]*counts
	owe map[rma.RMAID]int64
	// opened 标记欠额已初始化的授权单，避免 owe 扣到 0 后被重复重置。
	opened map[rma.RMAID]struct{}
}

func New(r *rma.RMA, fee int64, calc *refund.Calculator) (*Service, error) {
	if r == nil || calc == nil || fee < 0 || fee > 1_000_000_000 {
		return nil, ErrInvalid
	}
	s := &Service{
		r:      r,
		calc:   calc,
		fee:    fee,
		got:    make(map[lineKey]*counts),
		owe:    make(map[rma.RMAID]int64),
		opened: make(map[rma.RMAID]struct{}),
	}
	// 授权单到期时未扣完的欠额作废：清理该单残留，使同号重新授权时重新计费。
	r.SetExpiryHook(func(id rma.RMAID) {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.owe, id)
		delete(s.opened, id)
	})
	return s, nil
}

func (s *Service) Receive(id rma.RMAID, line rma.LineID, qty int64, g Grade, now int64) (Result, error) {
	if id == "" || qty < 1 || now < 0 || now > 1_000_000_000 || !g.valid() {
		return Result{}, ErrInvalid
	}

	due, deducted, paid, err := s.r.Receive(id, line, qty, now, g != C,
		func(qty int64, qualified bool, order rma.OrderID, line rma.LineID, paid, shipped int64) (int64, int64, int64) {
			// 本闭包在 rma 锁内执行；grade 记账由 s.mu 串行化。
			s.mu.Lock()
			defer s.mu.Unlock()

			key := lineKey{order, line}
			c := s.got[key]
			if c == nil {
				c = &counts{}
				s.got[key] = c
			}
			old := s.calc.Cumulative(paid, shipped, c.a, c.b)
			if qualified {
				switch g {
				case A:
					c.a += qty
				case B:
					c.b += qty
				}
			}
			var due int64
			if qualified {
				due = s.calc.Cumulative(paid, shipped, c.a, c.b) - old
			}

			owe, opened := s.owe[id]
			if !opened {
				owe = s.fee
				s.opened[id] = struct{}{}
			}
			d := due
			if d > owe {
				d = owe
			}
			owe -= d
			s.owe[id] = owe
			return due, d, due - d
		})
	if err != nil {
		return Result{}, err
	}
	return Result{Due: due, Deducted: deducted, Paid: paid}, nil
}
