// Package api 是驻留池的对外接口。依赖 pool。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/pool"
)

// Entry 是条目的只读快照（值 + 引用计数）。
type Entry = pool.Entry

// 可判定哨兵错误，四者互不相同。
var (
	ErrInvalidMaxEntries = errors.New("api: maxEntries 必须为正整数")
	ErrFull              = pool.ErrFull
	ErrNotInterned       = pool.ErrNotInterned
	ErrDoubleRelease     = pool.ErrDoubleRelease
)

var p *pool.Pool

// New 初始化容量为 maxEntries 的驻留池；maxEntries <= 0 时报错且不影响现有状态。
func New(maxEntries int) error {
	if maxEntries <= 0 {
		return ErrInvalidMaxEntries
	}
	p = pool.New(maxEntries)
	return nil
}

// Intern 驻留 s，返回驻留值（值恒等于 s）。
func Intern(s string) (string, error) { return p.Intern(s) }
func Release(s string) error          { return p.Release(s) }
func Len() int                        { return p.Len() }

// Snapshot 按 MRU→LRU 顺序返回所有条目的只读快照。
func Snapshot() []Entry { return p.Snapshot() }

// SelfCheck 用内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量 1、2：确定性随机序列 vs 朴素重放；驻留唯一；Len <= max。
	const max = 8
	q := pool.New(max)
	n := &naive{max: max, refs: map[string]int{}}
	r := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for i := 0; i < 3000; i++ {
		s := keys[r.Intn(len(keys))]
		var err, nerr error
		if r.Intn(2) == 0 {
			_, err = q.Intern(s)
			nerr = n.intern(s)
		} else {
			err = q.Release(s)
			nerr = n.release(s)
		}
		snap := q.Snapshot()
		if (err == nil) != (nerr == nil) || len(snap) > max || len(snap) != len(n.refs) {
			return fmt.Errorf("api: 第 %d 步与朴素参照不一致", i)
		}
		seen := map[string]bool{}
		for _, e := range snap {
			if seen[e.Value] || n.refs[e.Value] != e.Refs {
				return fmt.Errorf("api: 第 %d 步驻留唯一性或计数被破坏", i)
			}
			seen[e.Value] = true
		}
	}
	// 不变量 3、4：驱逐保护 + 失败不留痕。
	z := pool.New(2)
	z.Intern("x")
	z.Intern("y")
	before := z.Snapshot()
	if _, err := z.Intern("z"); !errors.Is(err, ErrFull) {
		return errors.New("api: 池满未报 ErrFull")
	}
	if err := z.Release("nope"); !errors.Is(err, ErrNotInterned) {
		return errors.New("api: 未驻留未报 ErrNotInterned")
	}
	if !slices.Equal(before, z.Snapshot()) { // 两次被拒后状态不变
		return errors.New("api: 被拒操作改变了状态")
	}
	z.Release("x") // x:0
	if err := z.Release("x"); !errors.Is(err, ErrDoubleRelease) {
		return errors.New("api: 双重释放未报 ErrDoubleRelease")
	}
	if _, err := z.Intern("z"); err != nil { // 驱逐 x，y(计数>0) 必须存活
		return err
	}
	if snap := z.Snapshot(); len(snap) != 2 || snap[0].Value != "z" || snap[1].Value != "y" || snap[1].Refs != 1 {
		return errors.New("api: 驱逐保护被破坏")
	}
	return pool.New(1).SelfCheck() // 驱逐扫描条目数不随 m 增长
}

// naive 朴素参照：逐条重放 Intern/Release/驱逐规则。
type naive struct {
	max   int
	refs  map[string]int
	order []string // MRU→LRU
}

func (n *naive) intern(s string) error {
	if _, ok := n.refs[s]; ok {
		n.refs[s]++
		n.touch(s)
		return nil
	}
	if len(n.refs) >= n.max {
		i := len(n.order) - 1
		for ; i >= 0 && n.refs[n.order[i]] != 0; i-- {
		}
		if i < 0 {
			return ErrFull
		}
		delete(n.refs, n.order[i])
		n.order = append(n.order[:i], n.order[i+1:]...)
	}
	n.refs[s] = 1
	n.touch(s)
	return nil
}

func (n *naive) release(s string) error {
	r, ok := n.refs[s]
	if !ok {
		return ErrNotInterned
	}
	if r == 0 {
		return ErrDoubleRelease
	}
	n.refs[s]--
	return nil
}

// touch 把 s 移到 MRU 端（不在表中则直接插入 MRU 端）。
func (n *naive) touch(s string) {
	for i, v := range n.order {
		if v == s {
			n.order = append(n.order[:i], n.order[i+1:]...)
			break
		}
	}
	n.order = append([]string{s}, n.order...)
}
