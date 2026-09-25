// Package api 是对外门面：参数校验、容量上限、哨兵错误、自检。
// 依赖 store，单向依赖。
package api

import (
	"errors"

	"ontology/store"
)

// 可判定哨兵错误，三者互不相同，用 errors.Is 判定。
var (
	ErrNonPositiveMaxDeltas = errors.New("maxDeltas 必须为正整数")
	ErrEmptyKey             = errors.New("Key 不能为空串")
	ErrTooManyDeltas        = errors.New("Apply 将使 delta 条目总数超过 maxDeltas")
)

// DeltaStore 是增量 delta 编码存储的对外实例。
type DeltaStore struct {
	max int
	st  *store.Store
}

// New 创建实例；maxDeltas 非正时返回 ErrNonPositiveMaxDeltas 且不产生任何状态。
func New(maxDeltas int) (*DeltaStore, error) {
	if maxDeltas <= 0 {
		return nil, ErrNonPositiveMaxDeltas
	}
	return &DeltaStore{max: maxDeltas, st: store.New()}, nil
}

// Apply 追加一条 delta（可为负或零）。Key 为空或会超容量时整体失败、不留痕。
func (d *DeltaStore) Apply(key string, delta int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if d.st.Apply(key, delta, d.max) {
		return ErrTooManyDeltas
	}
	return nil
}

// Get 返回 base[key] + Σ(delta[key])；未知 Key 为 0。
func (d *DeltaStore) Get(key string) int64 {
	return d.st.Get(key)
}

// Compact 把全部 delta 一次性合并进基线并清空日志；对所有 Key 同时生效。
func (d *DeltaStore) Compact() error {
	d.st.Compact()
	return nil
}

// DeltaCount 返回当前未合并 delta 条目总数。
func (d *DeltaStore) DeltaCount() int {
	return d.st.DeltaCount()
}

// SelfCheck 在独立的内部实例上跑内置操作序列，核验四条不变量。
// 不读写接收者的任何状态，可与 Get/DeltaCount 并发调用。
func (d *DeltaStore) SelfCheck() error {
	s, err := New(64)
	if err != nil {
		return err
	}
	// 不变量 1+3：与朴素参照一致（含负/零 delta）。
	ops := []struct {
		key string
		d   int64
	}{{"a", 10}, {"b", 20}, {"a", -4}, {"a", 0}, {"b", -5}, {"a", 7}}
	naive := map[string]int64{}
	for _, op := range ops {
		if err := s.Apply(op.key, op.d); err != nil {
			return err
		}
		naive[op.key] += op.d
	}
	for k, want := range naive {
		if got := s.Get(k); got != want {
			return errors.New("不变量1/3 失败: Get 与朴素参照不一致")
		}
	}
	// 不变量 2：Compact 前后所有可见值逐字段一致。
	before := map[string]int64{"a": s.Get("a"), "b": s.Get("b")}
	if err := s.Compact(); err != nil {
		return err
	}
	if s.DeltaCount() != 0 {
		return errors.New("不变量2 失败: Compact 后 DeltaCount 非 0")
	}
	for k, want := range before {
		if got := s.Get(k); got != want {
			return errors.New("不变量2 失败: Compact 改变了可见值")
		}
	}
	// 不变量 4：被拒操作不留痕，且实例仍可正常使用。
	cnt, valA := s.DeltaCount(), s.Get("a")
	if err := s.Apply("", 1); !errors.Is(err, ErrEmptyKey) {
		return errors.New("不变量4 失败: 空 Key 未返回 ErrEmptyKey")
	}
	full, _ := New(1)
	_ = full.Apply("x", 1)
	if err := full.Apply("y", 1); !errors.Is(err, ErrTooManyDeltas) {
		return errors.New("不变量4 失败: 超容量未返回 ErrTooManyDeltas")
	}
	if _, err := New(0); !errors.Is(err, ErrNonPositiveMaxDeltas) {
		return errors.New("不变量4 失败: 非正 maxDeltas 未返回 ErrNonPositiveMaxDeltas")
	}
	if s.DeltaCount() != cnt || s.Get("a") != valA {
		return errors.New("不变量4 失败: 被拒操作改变了状态")
	}
	return s.Apply("a", 1) // 被拒后仍可使用
}
