package ontology

import (
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
)

var (
	ErrInvalidArgument        = errors.New("参数非法")
	ErrAccountExists          = errors.New("账户已存在")
	ErrAccountNotFound        = errors.New("账户不存在")
	ErrTransactionExists      = errors.New("事务已存在")
	ErrInvalidTransaction     = errors.New("事务不存在或已终结")
	ErrZeroDelta              = errors.New("增量为零")
	ErrPendingLimitReached    = errors.New("未决项数已达上限")
	ErrLowerBoundInsufficient = errors.New("下界不足")
	ErrUpperBoundExceeded     = errors.New("上界超出")
	ErrNoReservations         = errors.New("事务从未预留过")
)

type PossibleRange struct {
	Lower int64
	Upper int64
}

type ReadResult struct {
	Balance int64
	Certain bool
	PossibleRange
}

type accountState struct {
	balance      int64
	negativeSum  int64
	positiveSum  int64
	pendingCount int64
}

type account struct {
	id           string
	lower        int64
	upper        int64
	pendingLimit int64
	state        atomic.Pointer[accountState]
}

type transaction struct {
	mu       sync.Mutex
	finished bool
	accounts map[*account]*reservation
}

type reservation struct {
	negativeSum int64
	positiveSum int64
	count       int64
}

type Bank struct {
	mu           sync.RWMutex
	accounts     map[string]*account
	transactions map[int64]*transaction
	logger       atomic.Pointer[Logger]
	gateMu       sync.RWMutex
}

func NewBank() *Bank {
	bank := &Bank{
		accounts:     make(map[string]*account),
		transactions: make(map[int64]*transaction),
	}
	var defaultLogger Logger = NewStandardLogger(os.Stdout)
	bank.logger.Store(&defaultLogger)
	return bank
}

func (b *Bank) CreateAccount(id string, initial, lower, upper, pendingLimit int64) error {
	if id == "" || lower > initial || initial > upper || pendingLimit < 1 {
		b.logf("CreateAccount 输入={id:%q initial:%d lower:%d upper:%d pendingLimit:%d} 输出=拒绝 判定依据=账户参数非法", id, initial, lower, upper, pendingLimit)
		return ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.accounts[id]; exists {
		b.logf("CreateAccount 输入={id:%q initial:%d lower:%d upper:%d pendingLimit:%d} 输出=拒绝 判定依据=账户已存在", id, initial, lower, upper, pendingLimit)
		return ErrAccountExists
	}

	created := &account{
		id:           id,
		lower:        lower,
		upper:        upper,
		pendingLimit: pendingLimit,
	}
	created.state.Store(&accountState{balance: initial})
	b.accounts[id] = created
	b.logf("CreateAccount 输入={id:%q initial:%d lower:%d upper:%d pendingLimit:%d} 输出=接受 判定依据=L<=b<=H 且 M>=1", id, initial, lower, upper, pendingLimit)
	return nil
}

func (b *Bank) BeginTransaction(id int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.transactions[id]; exists {
		b.logf("BeginTransaction 输入={txn:%d} 输出=拒绝 判定依据=事务已存在", id)
		return ErrTransactionExists
	}

	b.transactions[id] = &transaction{accounts: make(map[*account]*reservation)}
	b.logf("BeginTransaction 输入={txn:%d} 输出=接受", id)
	return nil
}

func (b *Bank) Reserve(transactionID int64, accountID string, delta int64) error {
	b.mu.RLock()
	txn := b.transactions[transactionID]
	b.mu.RUnlock()
	if txn == nil {
		b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=事务已提交、已中止或不存在", transactionID, accountID, delta)
		return ErrInvalidTransaction
	}

	b.gateMu.RLock()
	defer b.gateMu.RUnlock()
	b.mu.RLock()
	defer b.mu.RUnlock()
	txn.mu.Lock()
	defer txn.mu.Unlock()

	if txn.finished {
		b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=事务已提交或已中止", transactionID, accountID, delta)
		return ErrInvalidTransaction
	}

	target := b.accounts[accountID]
	if target == nil {
		b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=账户不存在", transactionID, accountID, delta)
		return ErrAccountNotFound
	}

	if delta == 0 {
		b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=增量必须为非零整数", transactionID, accountID, delta)
		return ErrZeroDelta
	}

	for {
		current := target.state.Load()
		if current.pendingCount >= target.pendingLimit {
			b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=未决项数 %d 已达上限 %d", transactionID, accountID, delta, current.pendingCount, target.pendingLimit)
			return ErrPendingLimitReached
		}

		next := *current
		next.pendingCount++
		if delta < 0 {
			next.negativeSum += delta
			if !addIsAtLeast(next.balance, next.negativeSum, target.lower) {
				b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=预留后下端小于下界 %d", transactionID, accountID, delta, target.lower)
				return ErrLowerBoundInsufficient
			}
		} else {
			next.positiveSum += delta
			if !addIsAtMost(next.balance, next.positiveSum, target.upper) {
				b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=拒绝 判定依据=预留后上端大于上界 %d", transactionID, accountID, delta, target.upper)
				return ErrUpperBoundExceeded
			}
		}

		if target.state.CompareAndSwap(current, &next) {
			entry := txn.accounts[target]
			if entry == nil {
				entry = &reservation{}
				txn.accounts[target] = entry
			}
			entry.count++
			if delta < 0 {
				entry.negativeSum += delta
			} else {
				entry.positiveSum += delta
			}
			b.logf("Reserve 输入={txn:%d account:%q delta:%d} 输出=接受 判定依据=新增量所在一侧仍在界内；区间=[%d,%d]，未决项=%d", transactionID, accountID, delta, next.balance+next.negativeSum, next.balance+next.positiveSum, next.pendingCount)
			return nil
		}
	}
}

func (b *Bank) Commit(transactionID int64) error {
	return b.finishTransaction(transactionID, true)
}

func (b *Bank) Abort(transactionID int64) error {
	return b.finishTransaction(transactionID, false)
}

func (b *Bank) Read(accountID string) (ReadResult, error) {
	b.gateMu.RLock()
	defer b.gateMu.RUnlock()
	b.mu.RLock()
	defer b.mu.RUnlock()

	target := b.accounts[accountID]
	if target == nil {
		b.logf("Read 输入={account:%q} 输出=拒绝 判定依据=账户不存在", accountID)
		return ReadResult{}, ErrAccountNotFound
	}

	state := target.state.Load()
	if state.pendingCount == 0 {
		result := ReadResult{Balance: state.balance, Certain: true}
		b.logf("Read 输入={account:%q} 输出={余额:%d 确定:true} 判定依据=无未决项", accountID, state.balance)
		return result, nil
	}

	result := ReadResult{
		Balance: state.balance,
		Certain: false,
		PossibleRange: PossibleRange{
			Lower: state.balance + state.negativeSum,
			Upper: state.balance + state.positiveSum,
		},
	}
	b.logf("Read 输入={account:%q} 输出={余额不确定 区间:[%d,%d] 未决项:%d} 判定依据=下端=b+负增量和，上端=b+正增量和", accountID, result.Lower, result.Upper, state.pendingCount)
	return result, nil
}

func (b *Bank) finishTransaction(transactionID int64, commit bool) error {
	action := "Abort"
	if commit {
		action = "Commit"
	}

	b.mu.RLock()
	txn := b.transactions[transactionID]
	b.mu.RUnlock()
	if txn == nil {
		b.logf("%s 输入={txn:%d} 输出=拒绝 判定依据=事务不存在", action, transactionID)
		return ErrInvalidTransaction
	}

	txn.mu.Lock()
	if txn.finished {
		txn.mu.Unlock()
		b.logf("%s 输入={txn:%d} 输出=拒绝 判定依据=事务已终结", action, transactionID)
		return ErrInvalidTransaction
	}
	if len(txn.accounts) == 0 {
		txn.mu.Unlock()
		b.logf("%s 输入={txn:%d} 输出=拒绝 判定依据=事务从未预留过", action, transactionID)
		return ErrNoReservations
	}
	txn.mu.Unlock()

	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	txn.mu.Lock()
	defer txn.mu.Unlock()

	if txn.finished {
		b.logf("%s 输入={txn:%d} 输出=拒绝 判定依据=事务已终结", action, transactionID)
		return ErrInvalidTransaction
	}
	if len(txn.accounts) == 0 {
		b.logf("%s 输入={txn:%d} 输出=拒绝 判定依据=事务从未预留过", action, transactionID)
		return ErrNoReservations
	}

	for target, entry := range txn.accounts {
		for {
			current := target.state.Load()
			next := *current
			if commit {
				next.balance += entry.negativeSum + entry.positiveSum
			}
			next.negativeSum -= entry.negativeSum
			next.positiveSum -= entry.positiveSum
			next.pendingCount -= entry.count
			if target.state.CompareAndSwap(current, &next) {
				break
			}
		}
	}

	txn.finished = true
	txn.accounts = make(map[*account]*reservation)
	decision := "全部未决项已并入余额"
	if !commit {
		decision = "全部未决项已丢弃"
	}
	b.logf("%s 输入={txn:%d} 输出=接受 判定依据=%s", action, transactionID, decision)
	return nil
}

func (b *Bank) logf(format string, args ...any) {
	logger := b.logger.Load()
	if logger == nil {
		return
	}
	(*logger).Printf(format, args...)
}

func addIsAtLeast(left, right, minimum int64) bool {
	if right >= 0 {
		if minimum == math.MinInt64 && right > 0 {
			return true
		}
		return left >= minimum-right
	}
	if left < minimum {
		return false
	}
	if minimum == math.MinInt64 && left > minimum {
		return true
	}
	return right >= minimum-left
}

func addIsAtMost(left, right, maximum int64) bool {
	if right <= 0 {
		if maximum == math.MaxInt64 && right < 0 {
			return true
		}
		return left <= maximum-right
	}
	if left > maximum {
		return false
	}
	if maximum == math.MaxInt64 && left < maximum {
		return true
	}
	return right <= maximum-left
}
