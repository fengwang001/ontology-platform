package settlement

import (
	"log"
	"math/big"
	"os"
	"sort"
	"sync"
)

type Logger interface {
	Printf(format string, args ...any)
}

type System struct {
	mu sync.RWMutex

	businessDays []int
	businessSet  map[int]struct{}
	lastIndex    int

	maxFailDays int
	penaltyBPS  int64

	accounts        map[AccountID]*accountState
	totalCash       int64
	totalSecurities map[SecurityID]int64
	orders          map[uint64]*Order
	byDue           map[int]map[uint64]*Order
	dueHeap         dueMinHeap
	active          []*Order

	log Logger
}

type accountState struct {
	cash              int64
	holdings          map[SecurityID]int64
	penaltyPayable    int64
	penaltyReceivable int64
	compPayable       int64
	compReceivable    int64
}

var noopLogger = noopLog{}

type noopLog struct{}

func (noopLog) Printf(string, ...any) {}

func New(config Config, accounts []Account) (*System, error) {
	if len(config.BusinessDays) == 0 || config.MaxFailDays <= 0 || config.PenaltyBPS < 0 {
		return nil, ErrInvalidParameter
	}

	days := append([]int(nil), config.BusinessDays...)
	sort.Ints(days)
	businessSet := make(map[int]struct{}, len(days))
	for index, day := range days {
		if index > 0 && day == days[index-1] {
			return nil, ErrInvalidParameter
		}
		businessSet[day] = struct{}{}
	}

	states := make(map[AccountID]*accountState, len(accounts))
	cashTotal := new(big.Int)
	securityTotalsBig := make(map[SecurityID]*big.Int)
	for _, account := range accounts {
		if account.ID == "" {
			return nil, ErrInvalidParameter
		}
		if _, exists := states[account.ID]; exists {
			return nil, ErrInvalidParameter
		}
		if account.Cash < 0 {
			return nil, ErrInvalidParameter
		}
		cashTotal.Add(cashTotal, big.NewInt(account.Cash))
		holdings := make(map[SecurityID]int64, len(account.Holdings))
		for security, quantity := range account.Holdings {
			if security == "" || quantity < 0 {
				return nil, ErrInvalidParameter
			}
			holdings[security] = quantity
			if securityTotalsBig[security] == nil {
				securityTotalsBig[security] = new(big.Int)
			}
			securityTotalsBig[security].Add(securityTotalsBig[security], big.NewInt(quantity))
		}
		states[account.ID] = &accountState{
			cash:     account.Cash,
			holdings: holdings,
		}
	}
	if !cashTotal.IsInt64() {
		return nil, ErrInvalidParameter
	}
	securityTotals := make(map[SecurityID]int64, len(securityTotalsBig))
	for security, total := range securityTotalsBig {
		if !total.IsInt64() {
			return nil, ErrInvalidParameter
		}
		securityTotals[security] = total.Int64()
	}

	return &System{
		businessDays:    days,
		businessSet:     businessSet,
		lastIndex:       -1,
		maxFailDays:     config.MaxFailDays,
		penaltyBPS:      config.PenaltyBPS,
		accounts:        states,
		totalCash:       cashTotal.Int64(),
		totalSecurities: securityTotals,
		orders:          make(map[uint64]*Order),
		byDue:           make(map[int]map[uint64]*Order),
		active:          nil,
		log:             log.New(os.Stderr, "", log.LstdFlags),
	}, nil
}

func (s *System) SetLogger(logger Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if logger == nil {
		s.log = noopLogger
		return
	}
	s.log = logger
}
