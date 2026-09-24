// Package api 对外提供分层合并 KV 写入路径：New/Put/Del/Get/SelfCheck。依赖 tree。
package api

import (
	"errors"
	"fmt"

	"ontology/seg"
	"ontology/tree"
)

// 三类可判定错误，互不相同。
var (
	ErrParam    = tree.ErrParam    // 参数非法
	ErrEmptyKey = tree.ErrEmptyKey // key 为空串
	ErrCorrupt  = seg.ErrCorrupt   // 段损坏
)

// Engine 是对外句柄，并发安全。
type Engine struct{ t *tree.Tree }

// New 校验 (cap, fanout, maxLevel)，非法返回 ErrParam。
func New(cap, fanout, maxLevel int) (*Engine, error) {
	t, err := tree.New(cap, fanout, maxLevel)
	if err != nil {
		return nil, err
	}
	return &Engine{t: t}, nil
}

// Put 写入 key=val；key 为空串返回 ErrEmptyKey 且不改状态。
func (e *Engine) Put(key, val string) error { return e.t.Put(key, val) }

// Del 写入 key 的墓碑；key 为空串返回 ErrEmptyKey 且不改状态。
func (e *Engine) Del(key string) error { return e.t.Del(key) }

// Get 返回 key 的当前值；不存在或已删除返回 ("", false)。
func (e *Engine) Get(key string) (string, bool) { return e.t.Get(key) }

// SelfCheck 在内部新建实例上核验四条不变量，全部通过返回 nil；不改动 e 的状态。
func (e *Engine) SelfCheck() error {
	for _, c := range []func() error{checkSequence, checkReference, checkStructure, checkRejected} {
		if err := c(); err != nil {
			return err
		}
	}
	return nil
}

// checkSequence 不变量 2：第三节 17 步序列，第 4/9/12/13/16/17 步 Get 必须等于推导答案。
func checkSequence() error {
	tr, err := tree.New(2, 2, 1)
	if err != nil {
		return err
	}
	seq := []struct{ k, key, val string }{
		{"p", "a", "1"}, {"p", "b", "2"}, {"p", "c", "3"}, {"g", "c", "3"},
		{"p", "a", "9"}, {"p", "d", "4"}, {"d", "b", ""}, {"p", "e", "5"}, {"g", "b", "-"},
		{"p", "f", "6"}, {"p", "g", "7"}, {"g", "b", "-"}, {"g", "a", "9"}, {"p", "b", "8"},
		{"p", "h", "1"}, {"g", "b", "8"}, {"g", "h", "1"},
	}
	for i, o := range seq {
		switch o.k {
		case "p":
			_ = tr.Put(o.key, o.val)
		case "d":
			_ = tr.Del(o.key)
		case "g":
			v, ok := tr.Get(o.key)
			if !ok {
				v = "-"
			}
			if v != o.val {
				return fmt.Errorf("api: selfcheck step %d Get(%s)=%s want %s", i+1, o.key, v, o.val)
			}
		}
	}
	return nil
}

// checkReference 不变量 1：确定性伪随机操作序列与朴素 map 参照逐键一致。
func checkReference() error {
	tr, _ := tree.New(3, 3, 2)
	ref := map[string]string{}
	seed := uint64(20260925)
	rnd := func() uint64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 33 }
	for i := 0; i < 2000; i++ {
		k := fmt.Sprint("k", rnd()%40)
		switch rnd() % 3 {
		case 0:
			_ = tr.Del(k)
			delete(ref, k)
		default:
			v := fmt.Sprint("v", rnd()%1000)
			_ = tr.Put(k, v)
			ref[k] = v
		}
	}
	for i := 0; i < 45; i++ {
		k := fmt.Sprint("k", i)
		v, ok := tr.Get(k)
		if rv, rok := ref[k]; ok != rok || (ok && v != rv) {
			return fmt.Errorf("api: selfcheck reference mismatch at %s", k)
		}
	}
	return nil
}

// checkStructure 不变量 3：操作序列中每步结构不变量成立。
func checkStructure() error {
	tr, _ := tree.New(2, 2, 2)
	for i := 0; i < 500; i++ {
		_ = tr.Put(fmt.Sprint("k", i%17), fmt.Sprint("v", i))
		if err := tr.CheckStructure(); err != nil {
			return err
		}
	}
	return nil
}

// checkRejected 不变量 4：被拒操作不改状态，三类错误互不相同，之后可正常使用。
func checkRejected() error {
	tr, _ := tree.New(2, 2, 1)
	_ = tr.Put("a", "1")
	before := tr.Segments()
	if !errors.Is(tr.Put("", "x"), ErrEmptyKey) || !errors.Is(tr.Del(""), ErrEmptyKey) {
		return errors.New("api: selfcheck empty key not rejected")
	}
	if _, err := tree.New(0, 2, 1); !errors.Is(err, ErrParam) {
		return errors.New("api: selfcheck bad param not rejected")
	}
	if _, _, err := seg.LoadSegment([]byte{1, 'k', 9, 0}); !errors.Is(err, ErrCorrupt) {
		return errors.New("api: selfcheck corrupt segment not rejected")
	}
	if ErrEmptyKey == ErrParam || ErrParam == ErrCorrupt || ErrEmptyKey == ErrCorrupt {
		return errors.New("api: selfcheck error kinds not distinct")
	}
	if fmt.Sprint(before) != fmt.Sprint(tr.Segments()) {
		return errors.New("api: selfcheck rejected op changed state")
	}
	if v, ok := tr.Get("a"); !ok || v != "1" {
		return errors.New("api: selfcheck state corrupted after rejection")
	}
	return tr.Put("b", "2") // 拒绝后仍可正常使用
}
