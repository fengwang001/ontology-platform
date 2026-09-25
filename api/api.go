// Package api 是对外门面，只依赖 shard（shard 依赖 route，方向单向）。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/shard"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrInvalidN            = errors.New("api: partition count must be positive") // New 的 n 或 Rebalance 的 newN 非正
	ErrPartitionOutOfRange = errors.New("api: partition index out of range")     // GetPartition 的 p 不在 [0,N)
	ErrEmptyKey            = errors.New("api: key must not be empty")            // Put/Get/GetPartition 的 key 为空
)

// API 是亲和键分区存储的对外接口。
type API struct{ st *shard.Store }

// New 创建 n 个分区；n 非正时整体失败、不产生状态。
func New(n int) (*API, error) {
	st, err := shard.New(n)
	if err != nil {
		return nil, ErrInvalidN
	}
	return &API{st: st}, nil
}

// Put 写入 key→val；空 key 被拒且不改状态。
func (a *API) Put(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	a.st.Put(key, val)
	return nil
}

// Get 算术定位读取；空 key 被拒。命中 (val,true,nil)，否则 ("",false,nil)。
func (a *API) Get(key string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	v, ok := a.st.Get(key)
	return v, ok, nil
}

// GetPartition 模拟客户端持过期路由直问分区 p：p!=home 时恒回 moved+home
// （与 key 是否存在无关），p==home 时回该分区内的真实存在性；越界/空 key 整体失败。
func (a *API) GetPartition(p int, key string) (val string, found, moved bool, home int, err error) {
	if key == "" {
		return "", false, false, 0, ErrEmptyKey
	}
	if p < 0 || p >= a.st.N() {
		return "", false, false, 0, ErrPartitionOutOfRange
	}
	home = a.st.Home(key)
	if p != home {
		return "", false, true, home, nil
	}
	val, found = a.st.Inspect(p, key)
	return val, found, false, home, nil
}

// Rebalance 改变分区数并同步一次性迁移；newN 非正被拒且状态不变。
func (a *API) Rebalance(newN int) error {
	if newN <= 0 {
		return ErrInvalidN
	}
	return a.st.Rebalance(newN)
}

// Dump 返回各分区内容的深拷贝。
func (a *API) Dump() []map[string]string { return a.st.Snapshot() }

// equalBuckets 判定两份分桶内容逐 key 逐 value 相同。
func equalBuckets(x, y []map[string]string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		for k, v := range x[i] {
			if yv, ok := y[i][k]; !ok || yv != v {
				return false
			}
		}
	}
	return true
}

// SelfCheck 对一组内置操作序列核验四条不变量，全部成立返回 nil。
func (a *API) SelfCheck() error {
	x, err := New(2)
	if err != nil {
		return err
	}
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		if err := x.Put(k, strings.ToUpper(k)); err != nil {
			return err
		}
	}
	if err := x.Rebalance(3); err != nil {
		return err
	}
	// 不变量 1+3：每个 key 恰在 home 一份；含 (甲) Get(c)=C、(丙) p0 问 b→moved 2。
	for _, k := range keys {
		want := strings.ToUpper(k)
		if v, ok, _ := x.Get(k); !ok || v != want {
			return fmt.Errorf("selfcheck: Get(%s)=%q,%v want %q", k, v, ok, want)
		}
		h := x.st.Home(k)
		if v, found, moved, home, _ := x.GetPartition((h+1)%3, k); !moved || home != h || found || v != "" {
			return fmt.Errorf("selfcheck: redirect for %s wrong", k)
		}
	}
	// 不变量 2：与朴素重建一致。
	naive, _ := New(3)
	for _, k := range keys {
		_ = naive.Put(k, strings.ToUpper(k))
	}
	if !equalBuckets(x.Dump(), naive.Dump()) {
		return fmt.Errorf("selfcheck: naive rebuild mismatch")
	}
	// 不变量 4：三类拒绝错误可判定，且拒绝前后 Dump 全等。
	before := x.Dump()
	checks := []error{
		e1(New(0)), x.Rebalance(0), x.Put("", "z"),
		e5(x.GetPartition(9, "a")),
	}
	wants := []error{ErrInvalidN, ErrInvalidN, ErrEmptyKey, ErrPartitionOutOfRange}
	for i, e := range checks {
		if !errors.Is(e, wants[i]) || !equalBuckets(before, x.Dump()) {
			return fmt.Errorf("selfcheck: rejected op %d wrong or mutated state: %v", i, e)
		}
	}
	return nil
}

func e1(_ *API, err error) error                   { return err }
func e5(_ string, _, _ bool, _ int, e error) error { return e }
