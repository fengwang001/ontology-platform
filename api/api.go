// Package api 对外暴露带 TTL 上限的进程内状态存储。依赖 ttl。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/entry"
	"ontology/ttl"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrBadTTL        = errors.New("api: ttl must be > 0")
	ErrBackwardClock = ttl.ErrBackwardClock
	ErrEmptyKey      = ttl.ErrEmptyKey
)

// Store 是对外句柄，并发安全。
type Store struct {
	s *ttl.Store
}

// New 构造存储；ttl ≤ 0 返回 ErrBadTTL，不产生任何状态。
func New(ttlVal int64) (*Store, error) {
	if ttlVal <= 0 {
		return nil, ErrBadTTL
	}
	return &Store{s: ttl.New(ttlVal)}, nil
}

// Set 写入/更新 key 并置 Ts = now。
func (st *Store) Set(key, value string, now int64) error {
	return st.s.Set(key, value, now)
}

// Get 返回当前值；过期（含等于）立即删除。不刷新 Ts。
func (st *Store) Get(key string, now int64) (string, bool) {
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

// SelfCheck 对内置操作序列核验四条不变量；全部通过返回 nil。
// 只使用自建的新存储，不触碰接收者状态，可并发调用。
func (st *Store) SelfCheck() error {
	if err := selfCheckNaive(); err != nil {
		return err
	}
	if err := selfCheckBounded(); err != nil {
		return err
	}
	return selfCheckClockAndAtomic()
}

// 不变量 1：随机操作序列的 View 必须逐 Key 等于朴素判定结果。
func selfCheckNaive() error {
	s, _ := New(10)
	model := map[string]entry.Entry{}
	rng := rand.New(rand.NewSource(1))
	var now int64
	for i := 0; i < 2000; i++ {
		now += int64(rng.Intn(4))
		k := fmt.Sprintf("k%d", rng.Intn(50))
		switch rng.Intn(3) {
		case 0:
			v := fmt.Sprintf("v%d", i)
			_ = s.Set(k, v, now)
			model[k] = entry.Entry{Value: v, Ts: now}
		case 1:
			s.Get(k, now)
		case 2:
			_, _ = s.Sweep(now)
		}
	}
	got, _ := s.View(now)
	for k, r := range model {
		if entry.Expired(now, r.Ts, 10) {
			continue
		}
		if got[k] != r.Value {
			return fmt.Errorf("selfcheck: 朴素对拍不一致 key=%s", k)
		}
		delete(got, k)
	}
	if len(got) != 0 {
		return fmt.Errorf("selfcheck: 朴素对拍多出 key: %v", got)
	}
	return nil
}

// 不变量 2：Sweep 后保留的每个 key 都满足 now−Ts < TTL；过期 Get 不残留。
func selfCheckBounded() error {
	s, _ := New(10)
	_ = s.Set("a", "1", 0)
	_ = s.Set("b", "2", 9)
	if _, ok := s.Get("a", 10); ok { // 10−0=10≥10，过期即删
		return fmt.Errorf("selfcheck: 过期 Get 应 ok=false")
	}
	if n, _ := s.Sweep(19); n != 1 { // b:19−9=10≥10 删；a 已被惰性删除
		return fmt.Errorf("selfcheck: Sweep(19) 应删 1 个, 实删 %d", n)
	}
	if v, _ := s.View(19); len(v) != 0 {
		return fmt.Errorf("selfcheck: Sweep 后仍有残留: %v", v)
	}
	return nil
}

// 不变量 3+4：时钟单调；三类被拒操作不改变任何状态，之后仍可正常使用。
func selfCheckClockAndAtomic() error {
	if _, err := New(0); err != ErrBadTTL {
		return fmt.Errorf("selfcheck: New(0) 应返回 ErrBadTTL")
	}
	s, _ := New(10)
	_ = s.Set("x", "1", 100)
	if err := s.Set("y", "2", 99); err != ErrBackwardClock {
		return fmt.Errorf("selfcheck: 时钟回退应返回 ErrBackwardClock")
	}
	if err := s.Set("", "3", 101); err != ErrEmptyKey {
		return fmt.Errorf("selfcheck: 空 key 应返回 ErrEmptyKey")
	}
	if _, err := s.Sweep(50); err != ErrBackwardClock {
		return fmt.Errorf("selfcheck: Sweep 时钟回退应返回 ErrBackwardClock")
	}
	v, _ := s.View(100) // 被拒操作不留痕：只剩 x，时钟上界仍是 100
	if len(v) != 1 || v["x"] != "1" {
		return fmt.Errorf("selfcheck: 被拒操作改变了状态: %v", v)
	}
	if err := s.Set("z", "4", 101); err != nil { // 拒绝后仍可正常使用
		return fmt.Errorf("selfcheck: 拒绝后无法继续使用: %v", err)
	}
	return nil
}
