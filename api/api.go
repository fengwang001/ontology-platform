// Package api 是 TTL 状态存储的对外门面，依赖 ttl。
package api

import (
	"errors"

	"ontology/ttl"
)

// ErrBadTTL 表示 New 收到的 ttl ≤ 0。
var ErrBadTTL = errors.New("api: ttl must be > 0")

// 哨兵错误再导出，调用方只需导入 api 即可判定全部三类错误。
var (
	ErrEmptyKey      = ttl.ErrEmptyKey
	ErrBackwardClock = ttl.ErrBackwardClock
)

// Store 是 Key→Value 的进程内 TTL 存储，并发安全。
type Store struct {
	s *ttl.Store
}

// New 构造存储；lifetime ≤ 0 时返回 ErrBadTTL。
func New(lifetime int64) (*Store, error) {
	if lifetime <= 0 {
		return nil, ErrBadTTL
	}
	return &Store{s: ttl.New(lifetime)}, nil
}

// Set 写入/更新 key 的值并置 Ts=now。
func (st *Store) Set(key, value string, now int64) error {
	return st.s.Set(key, value, now)
}

// Get 读取 key；过期立即删除并返回 ok=false；不刷新 Ts。
func (st *Store) Get(key string, now int64) (string, bool, error) {
	return st.s.Get(key, now)
}

// Sweep 主动清理所有过期 key，返回删除个数。
func (st *Store) Sweep(now int64) (int, error) {
	return st.s.Sweep(now)
}

// View 返回所有未过期 key 的快照（等价于先 Sweep 再全量列出）。
func (st *Store) View(now int64) (map[string]string, error) {
	return st.s.View(now)
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (st *Store) SelfCheck() error {
	// 不变量 1：与朴素判定一致。确定性伪随机序列对拍朴素 map。
	s, _ := New(7)
	ref := map[string][2]any{}
	var now int64
	for i := 0; i < 300; i++ {
		now += int64(i % 5)
		k := "k" + string(rune('a'+i%23))
		switch i % 3 {
		case 0:
			if err := s.Set(k, "v", now); err != nil {
				return err
			}
			ref[k] = [2]any{"v", now}
		case 1:
			if _, _, err := s.Get(k, now); err != nil {
				return err
			}
		case 2:
			if _, err := s.Sweep(now); err != nil {
				return err
			}
		}
		view, err := s.View(now)
		if err != nil {
			return err
		}
		for k, e := range ref {
			if now-e[1].(int64) >= 7 {
				delete(ref, k)
			}
		}
		if len(view) != len(ref) {
			return errors.New("selfcheck: 与朴素判定不一致")
		}
		for k := range view {
			if _, ok := ref[k]; !ok {
				return errors.New("selfcheck: 与朴素判定不一致")
			}
		}
	}
	// 不变量 2：有界内存。Sweep 后无过期残留；Get 过期即删不残留。
	s2, _ := New(10)
	for i := 0; i < 50; i++ {
		_ = s2.Set("k"+string(rune('a'+i%26))+string(rune('0'+i/26)), "v", int64(i))
	}
	if n, _ := s2.Sweep(1000); n != 50 {
		return errors.New("selfcheck: Sweep 未清光过期 key")
	}
	if v, _ := s2.View(1000); len(v) != 0 {
		return errors.New("selfcheck: Sweep 后仍有过期残留")
	}
	s3, _ := New(10)
	_ = s3.Set("x", "1", 5)
	if _, ok, _ := s3.Get("x", 20); ok {
		return errors.New("selfcheck: Get 未判定过期")
	}
	if n, _ := s3.Sweep(20); n != 0 {
		return errors.New("selfcheck: Get 过期未删，残留被 Sweep 再次发现")
	}
	// 不变量 3+4：时钟单调、失败不留痕，且拒绝后仍可正常使用。
	s4, _ := New(10)
	_ = s4.Set("a", "1", 5)
	before, _ := s4.View(5)
	_ = s4.Set("", "x", 6)                 // ErrEmptyKey
	_ = s4.Set("b", "2", 4)                // ErrBackwardClock
	_, _, _ = s4.Get("a", 3)               // ErrBackwardClock
	if _, err := s4.Sweep(2); err == nil { // 拒绝后时钟上界不变：重放合法 now 仍可用
		return errors.New("selfcheck: 时钟回退未被拒绝")
	}
	after, _ := s4.View(5)
	if len(after) != len(before) {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	if v, ok, _ := s4.Get("a", 5); !ok || v != "1" {
		return errors.New("selfcheck: 被拒操作改变了值或 Ts")
	}
	return nil
}
