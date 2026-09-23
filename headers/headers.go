// Package headers 提供有序多重映射的头部集合：解析、规范化、增删改与安全回写。
//
// 保序：同名头部按出现顺序保存，不去重、不重排。
// 安全：任何含 CR/LF/NUL/控制字符（含编码形态）的值在入口即被拒绝，
// 绝不写出、也绝不静默删除后写出。
// 并发：只读操作可并发（RWMutex）；写操作互斥，调用方应串行组织写。
package headers

import (
	"strings"
	"sync"
	"sync/atomic"

	"ontology/policy"
)

// Config 是资源上限与回写配置。零值字段由 DefaultConfig 填充。
type Config struct {
	MaxHeaders int // 头部条数上限
	MaxName    int // 单个名字长度上限（字节）
	MaxValue   int // 单个值长度上限（字节，展开后计）
	MaxBytes   int // 输入总字节上限
	FoldWidth  int // 回写折行宽度，<=0 表示不折行
}

// DefaultConfig 返回一组保守的默认上限。
func DefaultConfig() Config {
	return Config{MaxHeaders: 256, MaxName: 128, MaxValue: 8192, MaxBytes: 1 << 20}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.MaxHeaders <= 0 {
		c.MaxHeaders = d.MaxHeaders
	}
	if c.MaxName <= 0 {
		c.MaxName = d.MaxName
	}
	if c.MaxValue <= 0 {
		c.MaxValue = d.MaxValue
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = d.MaxBytes
	}
	return c
}

type entry struct {
	name  string // 已规范化的名字
	value string // 已展开、trim、校验过的值
}

// Set 是有序多重映射的头部集合。
type Set struct {
	mu    sync.RWMutex
	reg   *policy.Registry
	cfg   Config
	ents  []entry
	index map[string][]int // 小写规范名 -> 位置列表（按出现顺序）
	norm  bool             // 解析/修改过程中是否发生过规范化改写

	findCompares atomic.Int64 // 非导出计数器：最近一次按名查找的桶内比较数
}

// New 创建空集合。reg 为 nil 时使用默认策略登记表。
func New(reg *policy.Registry, cfg Config) *Set {
	if reg == nil {
		reg = policy.NewRegistry(policy.Policy{})
	}
	return &Set{reg: reg, cfg: cfg.withDefaults(), index: map[string][]int{}}
}

// Config 返回集合生效的配置。
func (s *Set) Config() Config { return s.cfg }

func keyOf(name string) string { return strings.ToLower(name) }

// positions 返回名字对应的位置列表，并把桶内比较数计入 findCompares。
// 调用方必须持有锁。
func (s *Set) positions(name string) []int {
	pos := s.index[keyOf(name)]
	s.findCompares.Store(int64(len(pos))) // 桶内逐条比较，桶外由 map 定位，不计
	return pos
}

// LastFindCompares 返回最近一次按名查找的桶内比较数（测试复杂度用）。
func (s *Set) LastFindCompares() int {
	return int(s.findCompares.Load())
}

// Len 返回头部总条数（同名重复分别计数）。
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ents)
}

// Count 返回某名字的出现次数；不存在返回 0。
func (s *Set) Count(name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.positions(name))
}

// Has 报告名字是否存在（与"存在但值为空"可判定区分）。
func (s *Set) Has(name string) bool { return s.Count(name) > 0 }

// Normalized 报告解析或修改过程中是否发生过规范化改写。
func (s *Set) Normalized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.norm
}

// GetAll 按出现顺序返回某名字的全部值；不存在返回 nil。
func (s *Set) GetAll(name string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos := s.positions(name)
	if len(pos) == 0 {
		return nil
	}
	out := make([]string, len(pos))
	for i, p := range pos {
		out[i] = s.ents[p].value
	}
	return out
}

// Get 返回单值：重复时按该名字的 policy 归并（取首/取末/合并/报错）。
// 不存在返回 ("", nil)；用 Has/Count 区分"不存在"与"存在但为空"。
func (s *Set) Get(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos := s.positions(name)
	if len(pos) == 0 {
		return "", nil
	}
	vals := make([]string, len(pos))
	for i, p := range pos {
		vals[i] = s.ents[p].value
	}
	return policy.Single(s.reg.For(s.ents[pos[0]].name), vals)
}

// List 把某名字的全部出现按列表语义合并切分，返回元素序列。
func (s *Set) List(name string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos := s.positions(name)
	if len(pos) == 0 {
		return nil, nil
	}
	vals := make([]string, len(pos))
	for i, p := range pos {
		vals[i] = s.ents[p].value
	}
	merged, err := policy.Single(policy.Policy{List: true, Dup: policy.DupMerge}, vals)
	if err != nil {
		return nil, err
	}
	return splitList(merged)
}

// ByteLen 返回当前集合规范序列化后的总字节数。
func (s *Set) ByteLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 2 // 终止空行
	for _, e := range s.ents {
		n += len(e.name) + 2 + len(e.value) + 2
	}
	return n
}
