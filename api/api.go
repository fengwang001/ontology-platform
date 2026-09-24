// Package api 是对外门面：参数校验（非法 ttl、空 Key 透传）+ 委托 state 包。
package api

import (
	"errors"
	"fmt"

	"ontology/state"
)

// ErrInvalidTTL 非法 ttl（<=0）被拒绝；与 state.ErrEmptyKey 互不相同。
var ErrInvalidTTL = errors.New("api: ttl must be positive")

// Table 是并发安全的 TTL 状态表。
type Table struct {
	st *state.Table
}

// New 建表；ttl <= 0 返回 ErrInvalidTTL，不产生任何状态。
func New(ttl int64) (*Table, error) {
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	return &Table{st: state.New(ttl)}, nil
}

// Put 写入值，last = max(旧 last, eventTime)；空 Key 返回 state.ErrEmptyKey。
func (t *Table) Put(key, val string, eventTime int64) error {
	return t.st.Put(key, val, eventTime)
}

// Get 返回值；Key 不存在或已过期均为无匹配（ok=false）。
func (t *Table) Get(key string, now int64) (string, bool, error) {
	return t.st.Get(key, now)
}

// Cleanup 清除 now 时刻全部过期条目，返回清除个数。
func (t *Table) Cleanup(now int64) int {
	return t.st.Cleanup(now)
}

// SelfCheck 对内置 Put/Get/Cleanup 序列核验四条不变量，全部通过返回 nil。
func (t *Table) SelfCheck() error {
	for _, chk := range []func() error{checkNaive, checkExpiredNoMatch, checkMonotonic, checkRejectNoTrace} {
		if err := chk(); err != nil {
			return err
		}
	}
	return nil
}

// 不变量 1：与朴素参照一致（确定性 LCG 生成随机操作序列对照朴素模型）。
func checkNaive() error {
	tb, _ := New(10)
	type kv struct {
		val  string
		last int64
	}
	model := map[string]kv{}
	rng := uint64(12345)
	next := func(n int64) int64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int64(rng>>33) % n
	}
	for step := 0; step < 3000; step++ {
		key := fmt.Sprintf("k%d", next(8))
		now := next(80)
		switch next(3) {
		case 0: // Put
			et := next(60)
			v := fmt.Sprintf("v%d", step)
			if err := tb.Put(key, v, et); err != nil {
				return err
			}
			e := model[key]
			if et > e.last {
				e.last = et
			}
			e.val = v
			model[key] = e
		case 1: // Get
			got, hit, err := tb.Get(key, now)
			if err != nil {
				return err
			}
			e, exist := model[key]
			want := exist && now-e.last < 10
			if hit != want || (hit && got != e.val) {
				return fmt.Errorf("selfcheck naive: Get(%s,%d)=%v,%v want %v", key, now, got, hit, e)
			}
			if exist && !want {
				delete(model, key) // 惰性清除与朴素模型同步
			}
		case 2: // Cleanup 后表内只剩余未过期条目
			tb.Cleanup(now)
			for k, e := range model {
				if now-e.last >= 10 {
					delete(model, k)
				}
			}
			for k, e := range model {
				got, hit, _ := tb.Get(k, now-1) // now-1 必未过期
				if !hit || got != e.val {
					return fmt.Errorf("selfcheck naive: %s lost after Cleanup", k)
				}
			}
		}
	}
	return nil
}

// 不变量 2：过期后被匹配即无匹配。
func checkExpiredNoMatch() error {
	tb, _ := New(10)
	if err := tb.Put("k", "v", 100); err != nil {
		return err
	}
	if _, hit, _ := tb.Get("k", 110); hit { // now-last==ttl 边界
		return errors.New("selfcheck: expired entry still matched")
	}
	tb2, _ := New(10)
	tb2.Put("k", "v", 100)
	if n := tb2.Cleanup(110); n != 1 {
		return fmt.Errorf("selfcheck: Cleanup removed %d, want 1", n)
	}
	if _, hit, _ := tb2.Get("k", 0); hit {
		return errors.New("selfcheck: cleaned entry still present")
	}
	return nil
}

// 不变量 3：last 单调，迟到事件不回退。
func checkMonotonic() error {
	tb, _ := New(10)
	tb.Put("k", "A", 200)
	tb.Put("k", "B", 50) // 迟到事件：last 不得回退
	if _, hit, _ := tb.Get("k", 205); !hit { // 若回退到 50 则早已过期
		return errors.New("selfcheck: last regressed on late event")
	}
	return nil
}

// 不变量 4：失败不留痕——非法 ttl / 空 Key 被拒后状态不变。
func checkRejectNoTrace() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidTTL) {
		return errors.New("selfcheck: New(0) not rejected")
	}
	if ErrInvalidTTL == state.ErrEmptyKey {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	tb, _ := New(10)
	tb.Put("k", "v", 100)
	if err := tb.Put("", "x", 999); !errors.Is(err, state.ErrEmptyKey) {
		return errors.New("selfcheck: empty key not rejected")
	}
	if _, _, err := tb.Get("", 0); !errors.Is(err, state.ErrEmptyKey) {
		return errors.New("selfcheck: empty key Get not rejected")
	}
	if _, hit, _ := tb.Get("k", 105); !hit { // 状态不变，且 last 未被 999 推进
		return errors.New("selfcheck: state changed after reject")
	}
	if _, hit, _ := tb.Get("k", 110); hit {
		return errors.New("selfcheck: rejected put advanced last")
	}
	return nil
}
