// Package headers 提供消息头部的有序多重映射：
// 解析、规范化、增删改、安全回写。
//
// 并发约定：只读方法（Get/GetAll/Has/Count/Len/TotalBytes/
// Normalized/Bytes）可并发；写方法（Add/Set/Del）由互斥锁
// 保护，与只读方法混用安全，但"读-改-写"复合操作的原子性
// 需调用方自行加锁保证。
package headers

import (
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/policy"
	"ontology/token"
)

// Limits 是可配置的资源上限，0 表示不限制。
type Limits struct {
	MaxEntries  int // 头部条数上限
	MaxNameLen  int // 单个名字长度上限
	MaxValueLen int // 单个值长度上限
	MaxBytes    int // 输入总字节上限
}

// DefaultLimits 返回一组保守的默认上限。
func DefaultLimits() Limits {
	return Limits{MaxEntries: 1024, MaxNameLen: 256, MaxValueLen: 8192, MaxBytes: 1 << 20}
}

// Config 是解析与回写配置。
type Config struct {
	Width    int // 回写折行宽度，<=0 表示不折行
	Limits   Limits
	Policies *policy.Table
}

// DefaultConfig 返回默认配置：折行宽度 78，默认上限与策略表。
func DefaultConfig() *Config {
	return &Config{Width: 78, Limits: DefaultLimits(), Policies: policy.Default()}
}

type entry struct {
	name    string // 规范化后的名字
	value   string
	deleted bool
}

// Set 是有序多重映射：同名头部保持出现顺序，不去重、不重排。
type Set struct {
	mu         sync.RWMutex
	entries    []entry
	index      map[string][]int // 规范化名 -> 下标列表（升序）
	cmps       atomic.Int64     // 非导出计数器：按名查找比较的条目数
	normalized bool             // 解析/写入时是否发生过规范化改写
	cfg        Config
}

// New 创建空集合。cfg 为 nil 时使用默认配置。
func New(cfg *Config) *Set {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	c := *cfg
	if c.Policies == nil {
		c.Policies = policy.Default()
	}
	return &Set{index: make(map[string][]int), cfg: c}
}

// validate 校验名字与值并做上限检查；任何失败都不改变集合状态。
func (s *Set) validate(name, value string, adding int) error {
	if !token.IsValidName(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if !token.IsValidValue(value) {
		return fmt.Errorf("%w in value of %q", ErrForbiddenByte, name)
	}
	if token.HasForbiddenEscape(value) {
		return fmt.Errorf("%w: percent-escape of forbidden byte in %q", ErrForbiddenByte, name)
	}
	lim := s.cfg.Limits
	if lim.MaxNameLen > 0 && len(name) > lim.MaxNameLen {
		return fmt.Errorf("%w: %q (%d > %d)", ErrNameTooLong, name, len(name), lim.MaxNameLen)
	}
	if lim.MaxValueLen > 0 && len(value) > lim.MaxValueLen {
		return fmt.Errorf("%w: %q (%d > %d)", ErrValueTooLong, name, len(value), lim.MaxValueLen)
	}
	if lim.MaxEntries > 0 && s.liveCount()+adding > lim.MaxEntries {
		return fmt.Errorf("%w: %d > %d", ErrTooManyEntries, s.liveCount()+adding, lim.MaxEntries)
	}
	return nil
}

func (s *Set) liveCount() int {
	n := 0
	for _, e := range s.entries {
		if !e.deleted {
			n++
		}
	}
	return n
}

// Add 追加一条头部，保持出现顺序。值含禁止字符时可判定地拒绝，
// 拒绝后集合状态零变化。
func (s *Set) Add(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(name, value, 1); err != nil {
		return err
	}
	canonical := token.Canonical(name)
	if canonical != name {
		s.normalized = true
	}
	s.entries = append(s.entries, entry{name: canonical, value: value})
	s.index[canonical] = append(s.index[canonical], len(s.entries)-1)
	return nil
}

// Set 把同名头部替换为单个值（保持首次出现的位置）。
func (s *Set) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(name, value, 1); err != nil {
		return err
	}
	canonical := token.Canonical(name)
	if canonical != name {
		s.normalized = true
	}
	kept := false
	for i := range s.entries {
		if s.entries[i].name == canonical && !s.entries[i].deleted {
			if !kept {
				s.entries[i].value = value
				kept = true
			} else {
				s.entries[i].deleted = true
			}
		}
	}
	if !kept {
		s.entries = append(s.entries, entry{name: canonical, value: value})
	}
	s.rebuildIndex()
	return nil
}

// Del 删除同名全部头部，返回删除条数。
func (s *Set) Del(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	canonical := token.Canonical(name)
	n := 0
	for i := range s.entries {
		if s.entries[i].name == canonical && !s.entries[i].deleted {
			s.entries[i].deleted = true
			n++
		}
	}
	if n > 0 {
		s.rebuildIndex()
	}
	return n
}

func (s *Set) rebuildIndex() {
	s.index = make(map[string][]int, len(s.index))
	for i, e := range s.entries {
		if !e.deleted {
			s.index[e.name] = append(s.index[e.name], i)
		}
	}
}
