// Package schema 管理版本迁移函数：登记、整条链完整性检查、按链依次执行。
// 它不依赖任何其他包。
package schema

import (
	"errors"
	"fmt"
	"sync"
)

// MigrateFunc 把某个版本的字节迁移到下一个版本。
type MigrateFunc func([]byte) ([]byte, error)

// 可判定哨兵错误。
var (
	// ErrInvalidRegister 覆盖 from<1、fn 为空、同一 from 重复登记。
	ErrInvalidRegister = errors.New("schema: invalid migration registration")
	// ErrInvalidRange 覆盖空范围或负版本等非法调用。
	ErrInvalidRange = errors.New("schema: invalid version range")
	// ErrMissingMigration 表示链上某一步尚未登记。
	ErrMissingMigration = errors.New("schema: missing migration")
	// ErrMigrationFailed 包装迁移函数自身返回的错误；
	// errors.Is(err, ErrMigrationFailed) 判定本类，errors.Is(err, cause) 可取原始错误。
	ErrMigrationFailed = errors.New("schema: migration failed")
)

// Registry 保存 from -> (from -> from+1) 的迁移函数，并发安全。
type Registry struct {
	mu  sync.RWMutex
	fns map[int]MigrateFunc
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{fns: make(map[int]MigrateFunc)}
}

// Register 登记从 from 到 from+1 的迁移函数。
// 要求 from>=1、fn 非空且同一 from 只能登记一次；拒绝时不改任何状态。
func (r *Registry) Register(from int, fn MigrateFunc) error {
	if from < 1 || fn == nil {
		return fmt.Errorf("schema: from=%d: %w", from, ErrInvalidRegister)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.fns[from]; ok {
		return fmt.Errorf("schema: migration %d->%d already registered: %w", from, from+1, ErrInvalidRegister)
	}
	r.fns[from] = fn
	return nil
}

// chainLocked 在锁内取出 v..V-1 的全部迁移函数；
// 任一步缺失立即返回 ErrMissingMigration，且返回的切片为 nil（一个都不执行）。
func (r *Registry) chain(v, V int) ([]MigrateFunc, error) {
	if v < 1 || V < v {
		return nil, fmt.Errorf("schema: migrate %d->%d: %w", v, V, ErrInvalidRange)
	}
	if v == V {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := make([]MigrateFunc, 0, V-v)
	for from := v; from < V; from++ {
		fn, ok := r.fns[from]
		if !ok {
			return nil, fmt.Errorf("schema: migration %d->%d not registered: %w", from, from+1, ErrMissingMigration)
		}
		chain = append(chain, fn)
	}
	return chain, nil
}

// CheckChain 只检查从 v 到 V 的整条链是否完整，不调用任何迁移函数。
func (r *Registry) CheckChain(v, V int) error {
	_, err := r.chain(v, V)
	return err
}

// Migrate 先检查整条链（缺任一步则一个函数都不调用），
// 然后依次执行 fn_v..fn_{V-1}，每步输入是上一步输出。
// data 必须由调用方保证是独立拷贝；Migrate 不会原地修改输入。
// 任一步失败：返回同时可 errors.Is 到 ErrMigrationFailed 与原始错误的错误。
func (r *Registry) Migrate(data []byte, v, V int) ([]byte, error) {
	chain, err := r.chain(v, V)
	if err != nil {
		return nil, err
	}
	out := data
	for i, fn := range chain {
		next, runErr := fn(out)
		if runErr != nil {
			return nil, fmt.Errorf("schema: step %d->%d: %w: %w", v+i, v+i+1, ErrMigrationFailed, runErr)
		}
		out = next
	}
	return out, nil
}
