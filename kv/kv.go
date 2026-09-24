// Package kv 保存 (schema 版本, 字节) 形式的键控状态，并在读取时惰性迁移、成功后原子写回。
// 它依赖 schema 包，反向依赖不允许。
package kv

import (
	"errors"
	"fmt"
	"sync"

	"ontology/schema"
)

// 可判定哨兵错误。
var (
	// ErrInvalidVersion：Upgrade 的目标版本不大于当前版本。
	ErrInvalidVersion = errors.New("kv: invalid schema version")
	// ErrKeyNotFound：读取一个从未 Write 且不存在的键。
	ErrKeyNotFound = errors.New("kv: key not found")
)

type entry struct {
	ver int
	raw []byte
}

// flight 表示某个键上正在执行的唯一一条迁移链；其余读者与写者在 done 上等待。
type flight struct {
	done   chan struct{}
	target int // 链开始时的当前 V，即本次 Read 线性化点上的目标版本
	val    []byte
	err    error
}

// Store 是进程内 (版本, 字节) 存储，并发安全。
type Store struct {
	mu sync.Mutex
	// reg 提供整链检查与迁移执行；其内部有独立的锁。
	reg *schema.Registry
	V   int
	m   map[string]*entry
	// flying 记录每个键当前在飞的迁移；同键同时刻至多一条链。
	flying map[string]*flight
	// touched 记录最近一次 Upgrade 或 Read 访问过的键个数（非导出，仅供包内测试核验惰性）。
	touched int
}

// New 创建当前版本为 1 的空存储。
func New(reg *schema.Registry) *Store {
	return &Store{reg: reg, V: 1, m: make(map[string]*entry), flying: make(map[string]*flight)}
}

// Upgrade 只提升当前版本，不触碰任何键；to<=V 整体失败且状态不变。
func (s *Store) Upgrade(to int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = 0 // 升级不访问任何键，证明惰性。
	if to <= s.V {
		return fmt.Errorf("kv: upgrade %d->%d: %w", s.V, to, ErrInvalidVersion)
	}
	s.V = to
	return nil
}

// Version 返回当前 schema 版本。
func (s *Store) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.V
}

// Write 以当前版本存入数据的独立拷贝；与该键在飞的迁移互斥（等待其结束再写）。
func (s *Store) Write(key string, data []byte) {
	s.mu.Lock()
	c := s.flying[key]
	s.mu.Unlock()
	if c != nil {
		<-c.done
	}
	s.mu.Lock()
	s.m[key] = &entry{ver: s.V, raw: clone(data)}
	s.mu.Unlock()
}

// Read 返回键当前版本数据。键不存在返回 ErrKeyNotFound；版本相同直接返回拷贝；
// 版本更旧则先整链预检（缺失零调用），再从存储版本起逐版迁移；失败存储原样，
// 全部等待者共享同一次执行；成功后在临界区内原子写回 (V,结果)。
// flying 单飞保证同键同时刻至多一条链；Write 等在飞链结束，天然互斥。
func (s *Store) Read(key string) ([]byte, error) {
	s.mu.Lock()
	s.touched = 1
	if c := s.flying[key]; c != nil { // 已有链在跑：等待并共享其结果，绝不重跑。
		s.mu.Unlock()
		<-c.done
		return clone(c.val), c.err
	}
	e, ok := s.m[key]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("kv: read %q: %w", key, ErrKeyNotFound)
	}
	if e.ver == s.V {
		raw := clone(e.raw)
		s.mu.Unlock()
		return raw, nil
	}
	// 先检查整条链：任一步未登记就返回，一个迁移函数都不调用。
	if err := s.reg.CheckChain(e.ver, s.V); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	c := &flight{done: make(chan struct{}), target: s.V}
	s.flying[key] = c
	s.mu.Unlock()

	// 锁外执行链：输入是存储字节的独立拷贝，只从 e.ver 起，绝不从版本 1 重跑。
	out, err := s.reg.Migrate(clone(e.raw), e.ver, c.target)

	s.mu.Lock()
	delete(s.flying, key)
	if err == nil && s.m[key] == e { // 期间无 Write（Write 等 done）；成功才整体替换。
		s.m[key] = &entry{ver: c.target, raw: out}
	}
	c.val, c.err = out, err
	s.mu.Unlock()
	close(c.done)          // 唤醒全部等待者，共享同一份结果与错误类别。
	return clone(out), err // 失败时未写回任何中间结果，存储保持原样。
}

// Stored 返回该键当前存储的版本与字节的独立拷贝；键不存在返回 ErrKeyNotFound。
func (s *Store) Stored(key string) (int, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return 0, nil, fmt.Errorf("kv: stored %q: %w", key, ErrKeyNotFound)
	}
	return e.ver, clone(e.raw), nil
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// touchedCount 仅供同包测试读取非导出惰性计数器，不出现在任何导出接口里。
func (s *Store) touchedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touched
}
