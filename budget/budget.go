// Package budget 在 cluster.Merger 之上施加按 (caller, tenant)
// 精确匹配的读授权：未授权调用者不能感知租户是否存在。
package budget

import (
	"errors"
	"strings"
	"sync"

	"ontology/cluster"
)

var (
	// ErrInvalidArgument 为参数非法（空串）。
	ErrInvalidArgument = errors.New("budget: invalid argument")
	// ErrUnauthorized 为调用者对该租户没有读授权。
	ErrUnauthorized = errors.New("budget: unauthorized")
)

// Entry 是 Templates 返回的单个模板。
type Entry struct {
	ID    int64
	Text  string
	Count int64
	Gen   int64
}

// Authorizer 是并发安全的读授权门面对象。
type Authorizer struct {
	mu       sync.RWMutex
	grants   map[string]map[string]struct{}
	delegate *cluster.Merger
}

// New 用底层归并器构造授权器。
func New(m *cluster.Merger) *Authorizer {
	return &Authorizer{
		grants:   make(map[string]map[string]struct{}),
		delegate: m,
	}
}

// Grant 登记 caller 对 tenant 的读授权，幂等。
func (a *Authorizer) Grant(caller, tenant string) error {
	if caller == "" || tenant == "" {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	set := a.grants[caller]
	if set == nil {
		set = make(map[string]struct{})
		a.grants[caller] = set
	}
	set[tenant] = struct{}{}
	return nil
}

// Templates 返回按 id 升序的模板与溢出桶计数。
// 拒绝顺序固定为：参数非法 → 未授权 → 租户不存在。
func (a *Authorizer) Templates(caller, tenant string) ([]Entry, int64, error) {
	if caller == "" || tenant == "" {
		return nil, 0, ErrInvalidArgument
	}
	a.mu.RLock()
	set := a.grants[caller]
	_, allowed := set[tenant]
	a.mu.RUnlock()
	if !allowed {
		return nil, 0, ErrUnauthorized
	}

	infos, overflow, err := a.delegate.Snapshot(tenant)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Entry, len(infos))
	for i, info := range infos {
		out[i] = Entry{
			ID:    info.ID,
			Text:  strings.Join(info.Text, " "),
			Count: info.Count,
			Gen:   info.Gen,
		}
	}
	return out, overflow, nil
}
