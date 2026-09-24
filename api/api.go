// Package api 对外提供 JSON Patch 的生成、应用与自检。
package api

import (
	"encoding/json"
	"fmt"
	"reflect"

	"ontology/patch"
	"ontology/ptr"
)

// Op 是补丁条目（patch.Op 的别名）。
type Op = patch.Op

// 四类可判定哨兵错误，互不相同。
var (
	ErrInvalidPath = ptr.ErrInvalidPath
	ErrNotFound    = ptr.ErrNotFound
	ErrInvalidOp   = patch.ErrInvalidOp
	ErrTooManyOps  = patch.ErrTooManyOps
)

// Handler 持有补丁条数上限；零值不可用，请用 New。
type Handler struct{ maxOps int }

// New 返回 maxOps 上限的 Handler。
func New(maxOps int) *Handler { return &Handler{maxOps: maxOps} }

// Diff 生成从 a 到 b 的补丁，不修改入参。
func (h *Handler) Diff(a, b any) ([]Op, error) { return patch.Diff(a, b, h.maxOps) }

// Apply 原子地应用补丁，返回新文档，不修改入参。
func (h *Handler) Apply(doc any, ops []Op) (any, error) { return patch.Apply(doc, ops, h.maxOps) }

// SelfCheck 对内置文档对核验四条不变量，全部通过返回 nil。
func (h *Handler) SelfCheck() error {
	pairs := [][2]any{
		{j(`{"a":1,"b":[1,2],"c":{"x":null}}`), j(`{"a":2,"b":[1],"c":{"y":"~1/"},"d":null}`)},
		{j(`{}`), j(`{"k":{}}`)},
		{j(`{"~1":true,"a/b":1}`), j(`{"~1":false,"a/b":{"z":[null]}}`)},
		{j(`[1,[2,[3]]]`), j(`{"0":[0]}`)},
	}
	for i, pr := range pairs {
		a, b := pr[0], pr[1]
		ops, err := h.Diff(a, b)
		if err != nil {
			return fmt.Errorf("selfcheck diff %d: %w", i, err)
		}
		got, err := h.Apply(a, ops)
		if err != nil {
			return fmt.Errorf("selfcheck apply %d: %w", i, err)
		}
		if !reflect.DeepEqual(got, b) { // 不变量 1：往返相等
			return fmt.Errorf("selfcheck %d: roundtrip mismatch", i)
		}
		if n, _ := h.Diff(a, a); len(n) != 0 { // 不变量 2：Diff(A,A) 为空
			return fmt.Errorf("selfcheck %d: Diff(A,A) not empty", i)
		}
		sa, sb := snapshot(a), snapshot(b)
		bad := append([]Op{}, ops...) // 不变量 4：失败不留痕
		bad = append(bad, Op{Op: "remove", Path: "/no/such/key"})
		if _, err := h.Apply(a, bad); err == nil {
			return fmt.Errorf("selfcheck %d: bad patch accepted", i)
		}
		if snapshot(a) != sa || snapshot(b) != sb { // 不变量 3：输入不被修改
			return fmt.Errorf("selfcheck %d: input mutated", i)
		}
	}
	return nil
}

func j(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}

func snapshot(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
