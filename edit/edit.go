// Package edit 在两个行序列之间求最短编辑脚本（Myers O(ND)）。
//
// 并列最短路径固定取「删除优先」：对角线推进时向下（删除 a 的行）与向右
//（插入 b 的行）等代价的情形优先删除，因此结果确定、可复现（见 DESIGN.md 3）。
package edit

import (
	"bytes"
	"errors"
)

// Kind 是一个脚本操作的种类。
type Kind uint8

const (
	// Equal 表示 a 与 b 共有的未改动行。
	Equal Kind = iota
	// Delete 表示仅存在于 a、被删除的行。
	Delete
	// Insert 表示仅存在于 b、被插入的行。
	Insert
)

// Op 是编辑脚本中的一步。Equal/Delete 时 Line 取自 a，Insert 时取自 b。
type Op struct {
	Kind Kind
	Line []byte
}

// ErrTooDifferent 表示最短编辑距离超过调用方给定的上限（差异过大）。
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// steps 记录最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
var steps int64

// Steps 返回最近一次 Diff 的对角线前进步数。
func Steps() int64 { return steps }

// Diff 返回把 a 变成 b 的最短编辑脚本。maxDist>0 时作为编辑距离上限，
// 超过即返回 ErrTooDifferent；maxDist<=0 表示不限制。
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	limit := n + m
	if maxDist > 0 && maxDist < limit {
		limit = maxDist
	}
	off := n + m
	v := make([]int, 2*off+3)
	v[off+1] = 0
	traces := make([][]int, 0, 16)
	steps = 0

	var found bool
	var d, fx, fy int
	for d = 0; d <= limit; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		traces = append(traces, snap)
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // 向右（插入 b 行）
			} else {
				x = v[off+k-1] + 1 // 向下（删除 a 行）：并列时优先
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				steps++
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				fx, fy, found = x, y, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return nil, ErrTooDifferent
	}

	var rev []Op
	x, y := fx, fy
	for ; d >= 0; d-- {
		vp := traces[d]
		k := x - y
		var pk int
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
		for x > px && y > py {
			rev = append(rev, Op{Equal, a[x-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				rev = append(rev, Op{Insert, b[y-1]})
			} else {
				rev = append(rev, Op{Delete, a[x-1]})
			}
		}
		x, y = px, py
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}
