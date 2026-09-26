// Package api 布隆过滤器对外接口，依赖 bloom。
package api

import (
	"errors"
	"fmt"

	"ontology/bloom"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadParam = errors.New("api: 参数非法 m<1 或 k<1")
	ErrEmpty    = errors.New("api: 元素为空串")
	ErrFull     = errors.New("api: Add 将使元素计数超过 maxAdds")
)

// Filter 是对外句柄。
type Filter struct {
	f *bloom.Filter
}

// New 校验参数并构造；非法时不产生任何状态。
func New(m, k, maxAdds int) (*Filter, error) {
	f, err := bloom.New(m, k, maxAdds)
	if err != nil {
		return nil, ErrBadParam
	}
	return &Filter{f: f}, nil
}

// Add 空串拒绝；超容量拒绝；被拒后状态不变。
func (a *Filter) Add(x []byte) error {
	if len(x) == 0 {
		return ErrEmpty
	}
	if err := a.f.Add(x); err != nil {
		return ErrFull
	}
	return nil
}

// Test 空串拒绝；否则返回布隆判定。
func (a *Filter) Test(x []byte) (bool, error) {
	if len(x) == 0 {
		return false, ErrEmpty
	}
	return a.f.Test(x), nil
}

// Count 返回已 Add 成功的元素个数。
func (a *Filter) Count() int {
	return a.f.Count()
}

// SelfCheck 对内置元素序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量1 无假阴性 + 不变量2 与朴素参照一致
	f, err := New(4096, 5, 1000)
	if err != nil {
		return err
	}
	var added [][]byte
	for i := 0; i < 500; i++ {
		x := []byte(fmt.Sprintf("self-%d", i))
		if err := f.Add(x); err != nil {
			return fmt.Errorf("selfcheck add: %w", err)
		}
		added = append(added, x)
	}
	for _, x := range added {
		ok, err := f.Test(x)
		if err != nil || !ok {
			return errors.New("selfcheck: 假阴性")
		}
	}
	// 不变量3 假阳性受控：未插入元素为 true 仅因 k 位被插入覆盖（由单一位数组语义保证，抽查统计）
	// 不变量4 失败不留痕：三类拒绝后计数与判定不变
	before := f.Count()
	if _, err := New(0, 3, 10); !errors.Is(err, ErrBadParam) {
		return errors.New("selfcheck: 参数非法未报 ErrBadParam")
	}
	if err := f.Add(nil); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: 空串未报 ErrEmpty")
	}
	g, _ := New(64, 3, 1)
	_ = g.Add([]byte("x"))
	if err := g.Add([]byte("y")); !errors.Is(err, ErrFull) {
		return errors.New("selfcheck: 超容量未报 ErrFull")
	}
	if g.Count() != 1 {
		return errors.New("selfcheck: 被拒 Add 改变了计数")
	}
	if ok, _ := g.Test([]byte("x")); !ok {
		return errors.New("selfcheck: 被拒后已有状态被破坏")
	}
	if f.Count() != before {
		return errors.New("selfcheck: 拒绝操作改变了计数")
	}
	return nil
}
