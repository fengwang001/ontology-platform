// Package api 是对外入口：New 构造引擎，Diff/Apply 生成与应用补丁，SelfCheck 自检。
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"ontology/patch"
	"ontology/ptr"
)

// Op 是补丁条目，与 patch.Op 同型。
type Op = patch.Op

// Engine 持有补丁条数上限，并发安全（构造后不可变）。
type Engine struct{ maxOps int }

// New 构造引擎，maxOps 为补丁条数上限；Diff 生成 a→b 的补丁；Apply 原子地应用补丁，失败时调用方文档不变。
func New(maxOps int) *Engine                           { return &Engine{maxOps: maxOps} }
func (e *Engine) Diff(a, b any) ([]Op, error)          { return patch.Diff(a, b, e.maxOps) }
func (e *Engine) Apply(doc any, ops []Op) (any, error) { return patch.Apply(doc, ops, e.maxOps) }

func mustJSON(s string) any { // 内置字面量，必解析成功
	var v any
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

// SelfCheck 对内置文档对核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	pairs := [][2]string{
		{`{"a/b":1,"arr":[5,6,7,8],"m~n":{"x":1,"y":2},"z":null,"~1":true}`,
			`{"a/b":2,"arr":[5],"k":{"q":1},"m~n":{"y":3,"w":[]},"n":null,"~1":false}`},
		{`{"x":{"y":[1,2,{"z":3}]},"n":null}`, `{"x":{"y":[1,2,{"z":4}]}}`},
		{`[1,2,3]`, `{"root":"replaced"}`},
	}
	for i, p := range pairs {
		if err := e.checkPair(mustJSON(p[0]), mustJSON(p[1])); err != nil {
			return fmt.Errorf("pair %d: %w", i, err)
		}
	}
	return e.checkFailures()
}

// checkPair 核验不变量 1（往返相等）、2（最小且有序）、3（输入不被修改）。
func (e *Engine) checkPair(a, b any) error {
	before, _ := json.Marshal(a)
	ops, err := e.Diff(a, b)
	if err != nil {
		return err
	}
	got, err := e.Apply(a, ops)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, b) {
		return errors.New("round trip mismatch")
	}
	if n := naive(a, b); n != len(ops) {
		return fmt.Errorf("op count %d != naive %d", len(ops), n)
	}
	if same, err := e.Diff(a, a); err != nil || len(same) != 0 {
		return errors.New("Diff(a,a) not empty")
	}
	if err := ordered(ops); err != nil {
		return err
	}
	if after, _ := json.Marshal(a); !bytes.Equal(before, after) {
		return errors.New("input mutated")
	}
	return nil
}

// naive 朴素计数：键并集上仅一侧有计 1，两侧都是对象递归，两侧值不等计 1。
func naive(a, b any) int {
	if reflect.DeepEqual(a, b) {
		return 0
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return 1
	}
	n := 0
	for k, av := range am {
		if bv, ok := bm[k]; ok {
			n += naive(av, bv)
		} else {
			n++
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			n++
		}
	}
	return n
}

// ordered 核验路径按解码后键序列逐段字节序严格递增且互不为前缀。
func ordered(ops []Op) error {
	segCmp := func(a, b []string) int { // 仅比较公共段，公共段全等（含前缀情形）返回 0
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				return strings.Compare(a[i], b[i])
			}
		}
		return 0
	}
	var prev []string
	for _, op := range ops {
		segs, _ := ptr.Split(op.Path) // 自检只处理生成的合法路径
		if prev != nil {
			if segCmp(prev, segs) >= 0 { // 递减、重复或互为前缀（公共段全等）
				return fmt.Errorf("paths not increasing or prefixed: %v vs %v", prev, segs)
			}
		}
		prev = segs
	}
	return nil
}

// checkFailures 核验不变量 4：四类错误可判定，被拒后文档不变。
func (e *Engine) checkFailures() error {
	cases := []struct {
		ops  []Op
		want error
	}{
		{[]Op{{Op: "remove", Path: "bad"}}, ptr.ErrInvalidPath},
		{[]Op{{Op: "remove", Path: "/nope"}}, ptr.ErrNotFound},
		{[]Op{{Op: "add", Path: "/y"}}, patch.ErrInvalidOp},
		{[]Op{{Op: "remove", Path: "/x"}, {Op: "remove", Path: "/arr"}}, patch.ErrTooManyOps},
	}
	small := New(1) // maxOps=1，末例两条即超限
	for i, c := range cases {
		doc := mustJSON(`{"x":1,"arr":[1,2]}`)
		before, _ := json.Marshal(doc)
		if _, err := small.Apply(doc, c.ops); !errors.Is(err, c.want) {
			return fmt.Errorf("case %d: got %v want %v", i, err, c.want)
		}
		if after, _ := json.Marshal(doc); !bytes.Equal(before, after) {
			return fmt.Errorf("case %d: doc mutated after rejection", i)
		}
	}
	return nil
}
