package quota

import (
	"errors"
	"sync"
)

var (
	ErrInvalidQuota  = errors.New("参数非法")
	ErrBelowOccupied = errors.New("低于占用")
	ErrInsufficient  = errors.New("额度不足")
)

// Info 是某租户的账本视图。
type Info struct {
	Quota    int64
	Used     int64
	Reserved int64
}

// Ledger 记录每个租户的额度 q、已提交用量 U 与预留量 R。
type Ledger struct {
	mu       sync.Mutex
	quota    map[string]int64
	used     map[string]int64
	reserved map[string]int64
}

func New() *Ledger {
	return &Ledger{
		quota:    map[string]int64{},
		used:     map[string]int64{},
		reserved: map[string]int64{},
	}
}

func (l *Ledger) SetQuota(tenant string, q int64) error {
	if tenant == "" || q < 0 || q > 1e15 {
		return ErrInvalidQuota
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if q < l.used[tenant]+l.reserved[tenant] {
		return ErrBelowOccupied
	}
	l.quota[tenant] = q
	return nil
}

func (l *Ledger) Reserve(tenant string, total int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used[tenant]+l.reserved[tenant]+total > l.quota[tenant] {
		return ErrInsufficient
	}
	l.reserved[tenant] += total
	return nil
}

func (l *Ledger) Commit(tenant string, total, oldSize int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reserved[tenant] -= total
	l.used[tenant] += total - oldSize
}

func (l *Ledger) Release(tenant string, total int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reserved[tenant] -= total
}

func (l *Ledger) Info(tenant string) Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Info{
		Quota:    l.quota[tenant],
		Used:     l.used[tenant],
		Reserved: l.reserved[tenant],
	}
}

// Tenants 返回所有显式设置过额度的租户。
func (l *Ledger) Tenants() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.quota))
	for t := range l.quota {
		out = append(out, t)
	}
	return out
}
