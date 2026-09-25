// Package api 对外提供进程内的时间旅行 as-of 查询（MVCC）。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/chain"
	"ontology/ver"
)

// 三类互不相同的可判定哨兵错误。
var (
	ErrEmptyKey   = errors.New("key 不能为空串")
	ErrBadTime    = errors.New("ts 不能为负数")
	ErrEmptyValue = errors.New("value 不能为空串")
)

// DB 是进程内存中的多 key MVCC 存储。零值不可用，用 New 构造。
type DB struct {
	mu     sync.RWMutex
	chains map[string]*chain.Chain
}

// New 创建空 DB。
func New() *DB {
	return &DB{chains: map[string]*chain.Chain{}}
}

// Write 在 key 的版本链中插入值版本 (ts,value)。乱序 ts 合法；先校验后改状态。
func (d *DB) Write(key string, ts int64, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return ErrBadTime
	}
	if value == "" {
		return ErrEmptyValue
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.chains[key]
	if c == nil {
		c = &chain.Chain{}
		d.chains[key] = c
	}
	c.Insert(ver.ValueVersion(ts, value))
	return nil
}

// Delete 在 key 的版本链中插入 tombstone 版本 (ts,删除)。先校验后改状态。
func (d *DB) Delete(key string, ts int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return ErrBadTime
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.chains[key]
	if c == nil {
		c = &chain.Chain{}
		d.chains[key] = c
	}
	c.Insert(ver.Tombstone(ts))
	return nil
}

// AsOf 返回 key 在 T 时刻的样子：ts<=T 的最新版本；tombstone 或不存在则 ok=false。
func (d *DB) AsOf(key string, T int64) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	c := d.chains[key]
	if c == nil {
		return "", false
	}
	v, ok := c.AsOf(T)
	if !ok {
		return "", false
	}
	return v.Value, true
}

// ViewAsOf 返回所有 key 在 T 时刻的快照（逐 key 取 AsOf；当前已删的 key 不出现）。
func (d *DB) ViewAsOf(T int64) map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string]string, len(d.chains))
	for k, c := range d.chains {
		if v, ok := c.AsOf(T); ok {
			out[k] = v.Value
		}
	}
	return out
}

// SelfCheck 对一组内置操作序列核验四条不变量，任一条不成立即返回错误。
// 自检在内部新建 DB 上进行，不改变接收者状态。
func (d *DB) SelfCheck() error {
	s := New()
	K := "selfcheck"
	s.Write(K, 10, "a")
	s.Write(K, 30, "b")
	s.Write(K, 20, "c")
	if v, ok := s.AsOf(K, 15); !ok || v != "a" {
		return fmt.Errorf("AsOf(15) 应为 a，得 %q/%v", v, ok)
	}
	if v, ok := s.AsOf(K, 30); !ok || v != "b" {
		return fmt.Errorf("AsOf(30) 应为 b，得 %q/%v", v, ok)
	}
	s.Delete(K, 40)
	if v, ok := s.AsOf(K, 25); !ok || v != "c" {
		return fmt.Errorf("AsOf(25) 应为 c，得 %q/%v", v, ok)
	}
	if _, ok := s.AsOf(K, 40); ok {
		return errors.New("AsOf(40) 应为不存在（tombstone）")
	}
	if err := chain.SelfCheckCompares(); err != nil {
		return err
	}
	return nil
}
