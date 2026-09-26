// Package api 是对外接口层：参数校验、哨兵错误、自检。依赖 cbf。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/cbf"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrInvalidParam = errors.New("api: m and k must be positive")
	ErrInvalidKey   = errors.New("api: key must be non-negative")
	ErrNotPresent   = cbf.ErrNotPresent
)

// Filter 是计数布隆过滤器的对外句柄。
type Filter struct {
	f *cbf.Filter
	m int
	k int
}

// New 构造过滤器；m ≤ 0 或 k ≤ 0 报 ErrInvalidParam，不产生任何状态。
func New(m, k int) (*Filter, error) {
	if m <= 0 || k <= 0 {
		return nil, ErrInvalidParam
	}
	return &Filter{f: cbf.New(m, k), m: m, k: k}, nil
}

// Add 加入 key；key < 0 报 ErrInvalidKey，不改变任何计数器。
func (f *Filter) Add(key int64) error {
	if key < 0 {
		return ErrInvalidKey
	}
	f.f.Add(key)
	return nil
}

// Remove 删除 key；key < 0 报 ErrInvalidKey，key 不在集合报 ErrNotPresent，
// 两种失败都不改变任何计数器。
func (f *Filter) Remove(key int64) error {
	if key < 0 {
		return ErrInvalidKey
	}
	return f.f.Remove(key)
}

// Query 返回 key 的计数下界估计（≥1 存在，0 不存在）；key < 0 报 ErrInvalidKey。
func (f *Filter) Query(key int64) (int64, error) {
	if key < 0 {
		return 0, ErrInvalidKey
	}
	return f.f.Query(key), nil
}

// Snapshot 返回计数器数组副本（演示与测试用）。
func (f *Filter) Snapshot() []int64 { return f.f.Snapshot() }

// QueryLocalityOK 报告最近一次 Query 访问的计数器个数是否恰为 k（只给布尔结论）。
func (f *Filter) QueryLocalityOK() bool { return f.f.QueryLocalityOK() }

// SelfCheck 在内部全新实例上核验四条不变量，可被测试直接调用、可并发调用。
func (f *Filter) SelfCheck() error {
	const m, k = 64, 4
	// 不变量 1：无假阴性
	g := cbf.New(m, k)
	for x := int64(0); x < 32; x++ {
		g.Add(x)
	}
	for x := int64(0); x < 32; x++ {
		if g.Query(x) < 1 {
			return fmt.Errorf("selfcheck: false negative on %d", x)
		}
	}
	// 不变量 2：精确移除（无共享时计数器全部归 0）
	g = cbf.New(m, k)
	g.Add(7)
	if err := g.Remove(7); err != nil {
		return fmt.Errorf("selfcheck: remove present key: %w", err)
	}
	for i, v := range g.Snapshot() {
		if v != 0 {
			return fmt.Errorf("selfcheck: counter %d = %d after exact removal", i, v)
		}
	}
	// 不变量 3：与朴素重放一致
	g = cbf.New(m, k)
	model := make([]int64, m)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		key := rng.Int63n(40)
		if rng.Intn(2) == 0 {
			g.Add(key)
			for j := 1; j <= k; j++ {
				model[int((int64(j)*key)%m)]++
			}
		} else if err := g.Remove(key); err == nil {
			for j := 1; j <= k; j++ {
				model[int((int64(j)*key)%m)]--
			}
		}
	}
	for i, v := range g.Snapshot() {
		if v != model[i] {
			return fmt.Errorf("selfcheck: replay mismatch at %d: %d != %d", i, v, model[i])
		}
	}
	// 不变量 4：失败不留痕（在受控的新实例上，保证被删 key 有计数器为 0）
	if _, err := New(0, k); !errors.Is(err, ErrInvalidParam) {
		return fmt.Errorf("selfcheck: bad param: %v", err)
	}
	g = cbf.New(m, k)
	g.Add(0) // 只有 c[0] 非零；key=1 命中 c[1..4]，全为 0
	before := g.Snapshot()
	if err := g.Remove(1); !errors.Is(err, ErrNotPresent) {
		return fmt.Errorf("selfcheck: remove absent: %v", err)
	}
	for i, v := range g.Snapshot() {
		if v != before[i] {
			return fmt.Errorf("selfcheck: rejected op mutated counter %d", i)
		}
	}
	return nil
}
