package ledger

import "sync"

type account struct {
	limit     int64
	used      int64
	bucketCnt int64
}

type Ledger struct {
	mu       sync.RWMutex
	accounts map[string]*account
}

func New() *Ledger {
	return &Ledger{accounts: map[string]*account{}}
}

func (l *Ledger) acct(tenant string) *account {
	a, ok := l.accounts[tenant]
	if !ok {
		a = &account{}
		l.accounts[tenant] = a
	}
	return a
}

// SetLimit 总被接受，允许额度低于当前用量。
func (l *Ledger) SetLimit(tenant string, q int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acct(tenant).limit = q
}

func (l *Ledger) Limit(tenant string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.acct(tenant).limit
}

func (l *Ledger) Used(tenant string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.acct(tenant).used
}

func (l *Ledger) BucketCount(tenant string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.acct(tenant).bucketCnt
}

func (l *Ledger) AddUsed(tenant string, delta int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acct(tenant).used += delta
}

func (l *Ledger) IncBuckets(tenant string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acct(tenant).bucketCnt++
}

func (l *Ledger) DecBuckets(tenant string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acct(tenant).bucketCnt--
}

type AcctSnapshot struct {
	Limit     int64
	Used      int64
	BucketCnt int64
}

func (l *Ledger) Snapshot() map[string]AcctSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]AcctSnapshot, len(l.accounts))
	for t, a := range l.accounts {
		out[t] = AcctSnapshot{Limit: a.limit, Used: a.used, BucketCnt: a.bucketCnt}
	}
	return out
}
