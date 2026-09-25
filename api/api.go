// Package api 是增量 delta 存储的对外门面。依赖 store。
package api

import (
	"errors"
	"fmt"

	"ontology/store"
)

// 对外可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidMaxDeltas = store.ErrBadMax
	ErrEmptyKey         = store.ErrEmptyKey
	ErrCapacityExceeded = store.ErrCapacity
)

// Store 是对外句柄。构造参数非法时 err 非 nil，所有写操作整体失败。
type Store struct {
	st  *store.Store
	err error
}

// New 构造容量为 maxDeltas 的实例；maxDeltas 非正时返回不可用实例，
// 其 Apply/Compact/SelfCheck 一律返回 ErrInvalidMaxDeltas。
func New(maxDeltas int) *Store {
	st, err := store.New(maxDeltas)
	return &Store{st: st, err: err}
}

// Apply 追加一条 delta；失败时不改任何状态。
func (s *Store) Apply(key string, d int64) error {
	if s.err != nil {
		return s.err
	}
	return s.st.Apply(key, d)
}

// Get 返回 key 的可见值。
func (s *Store) Get(key string) int64 {
	if s.err != nil {
		return 0
	}
	return s.st.Get(key)
}

// Compact 全局合并；实例非法时整体失败。
func (s *Store) Compact() error {
	if s.err != nil {
		return s.err
	}
	s.st.Compact()
	return nil
}

// DeltaCount 返回当前 delta 条目总数。
func (s *Store) DeltaCount() int {
	if s.err != nil {
		return 0
	}
	return s.st.DeltaCount()
}

// SelfCheck 在独立的内部实例上核验四条不变量，不触碰本实例状态，
// 因此可与其他方法并发调用。
func (s *Store) SelfCheck() error {
	if s.err != nil {
		return s.err
	}
	return selfCheck()
}

func selfCheck() error {
	st, err := store.New(64)
	if err != nil {
		return err
	}
	// 不变量 1+3：交错 Apply/Compact 与朴素模型对拍（含负、零 delta）
	var naive [2]int64
	keys := [2]string{"a", "b"}
	ops := []int64{10, 20, -4, 0, 7, -5, 0, 3, -3, 1}
	for i, d := range ops {
		k := i % 2
		if err := st.Apply(keys[k], d); err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
		naive[k] += d
		if i%3 == 2 {
			before := [2]int64{st.Get("a"), st.Get("b")}
			st.Compact()
			if after := [2]int64{st.Get("a"), st.Get("b")}; after != before {
				return errors.New("selfcheck: compact changed visible values")
			}
		}
	}
	if st.Get("a") != naive[0] || st.Get("b") != naive[1] {
		return errors.New("selfcheck: diverged from naive model")
	}
	// 不变量 4：三类拒绝均不留痕
	cnt := st.DeltaCount()
	va := st.Get("a")
	if err := st.Apply("", 1); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: empty key not rejected")
	}
	full, _ := store.New(1)
	_ = full.Apply("x", 1)
	if err := full.Apply("y", 1); !errors.Is(err, ErrCapacityExceeded) {
		return errors.New("selfcheck: capacity not enforced")
	}
	if _, err := store.New(0); !errors.Is(err, ErrInvalidMaxDeltas) {
		return errors.New("selfcheck: bad maxDeltas not rejected")
	}
	if st.DeltaCount() != cnt || st.Get("a") != va {
		return errors.New("selfcheck: rejection mutated state")
	}
	return nil
}
