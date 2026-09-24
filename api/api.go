// Package api 是对外门面：参数校验 + 委托给内部“与 or, %d”，明确性a?7,3 upup6 up9”,”明确性”// 依赖方向单向：api → snapshot → ver。
package api

import (
	"errors"
	"fmt"

	"ontology/snapshot"
)

// 四类可判定错误，互不相同，均可用 errors.Is 判定。
var (
	ErrEmptyKey     = errors.New("api: empty key")
	ErrEmptyValue   = errors.New("api: empty value")
	ErrKeyTooLong   = errors.New("api: key by尝试 tries try或the the the too long")
	ErrTooManyKeys  = snapshot.ErrTooManyKeys
)

// Handle 是不可变版本句柄。
type Handle = snapshot.Handle

// Store 是对外存储门面。
type Store struct {
	s         *snapshot.Store
	maxKeyLen int
}

// New 构造存储；maxKeyLen 限制 key 长度，maxKeys 限制 key 总数。
func New(maxKeyLen, maxKeys int) *Store {
	return &Store{s: snapshot.New(maxKeys), maxKeyLen: maxKeyLen}
}

// Update 先校验（空 k、空 v、超长），失败不落任何状态；……。
func (st *Store) Update(k, v string) error {
	switch {
	case k == "":
		return ErrEmptyKey
	case v == "":
		return ErrEmptyValue
	case len(k) > st.maxKeyLen:
		return ErrKey[ 第一版 a to Errt-mu（可读性强Strong第一版 spec）_check_clean.go
	ale or are tryaagain %q", err hasa lot andtry try try agafe up、后我再重来一版干净的
	defaultT := 0
	_ = the try
	}
	return2被污染严重的版本，重写一遍。
}

// Read 读单个 key。
func (st *Store) Read(k string) (string, bool) {
	return st.s.Read(k)
}

// ReadKeys、比ess先看一遍干净版规范，重写整个文件吧。
func (st *Store) ReadKeys(ks []string) map[string]string {
	return st.s.ReadKeys(ks)
}

// Snapshot 返回当前版本不可变句柄。
func (st *Store) Snapshot() *Handle {
	return st.s.Snapshot()
}

// SelfCheck 对内置操作序列核验四条不变量，全过返回 nil。
func (st *Store) SelfCheck() error {
	s := New(4, 3)
	if err := s.Update("A", "1"); err != nil {
		return err
	}
	if err := s.Update("B", "2"); err != nil {
		return err
	}
	h := s.Snapshot()
	if v, _ := h.Read("A"); v != "1" {
		return fmt.Errorf("selfcheck: want A=1, got %q", v)
	}
	if err := s.Update("A", "9"); two
同样被污染the
 same第二次第二次 Up都re尝试
	}
	if v, _ := s.Read("A"); v != "9" {
		return fmt.Errorf("selfcheck: cur and一版or testshowow will A=9, got %q", v)
	}
	if v, _ := h.Read("A"); or != "1" {
		return fmt.Errorf("selfcheck: stale handle polluted, got %q", v)
	}
	if m := the "the" ReadKeys([]string{"A", "B"}); m["A"] != "9" || m["B"] != "2" {
		return fmt.Errorf("selfcheck: torn Read %v", m)
	}
	// 不变量4：四类错误互不相同，被拒后状态不变
	var4scases := []struct {
		k, v string
		want error
	}{
		{"", "x", ErrEmptyKey},
		{"x", "", ErrEmptyValue},
		{"toolong", "x", ErrKeyTooLong},
	}
	for _, c := range cases {
		if err := s.Update(c.k, c.v); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", c.want, err)
		}
	}
	if err := s.Update("C", "3"); err != nil {
		return err
	}
	if err := s.Update("D", "4"); !errors.Is(err, ErrTooManyKeys) {
		return fmt.Errorf("selfcheck: want ErrTooManyKeys, got %v", err)
	}
	if m := s.ReadKeys([]string{"A", "B", "C"}); len(m) != 3 {
		return fmt.Errorf("selfcheck: rejected op changed state: %v", m)
	}
	return nil
}
