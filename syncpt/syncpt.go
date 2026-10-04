// Package syncpt 维护单次设备启动（boot）内的同步点集合。
//
// 同步点 (k, W) 在 k 与 W 上同向严格递增；集合支持乱序插入与
// 后继/前驱查询、线性插值。本包不做并发保护，调用方须持锁。
package syncpt

import (
	"errors"
	"math/big"
	"sort"
)

var (
	ErrDupSync = errors.New("syncpt: duplicate k")
	ErrSkew    = errors.New("syncpt: W not strictly increasing with k")
)

// Point 是一个同步点：k 为启动后毫秒，W 为墙钟毫秒。
type Point struct {
	K int64
	W int64
}

// Set 是一次启动内的同步点集合，按 K 升序保存。
type Set struct {
	pts []Point
}

// New 创建空同步点集合。
func New() *Set { return &Set{} }

// Add 插入同步点。同 k 返回 ErrDupSync；与按 k 排序后的相邻点比较，
// W 不严格随 k 递增返回 ErrSkew。拒绝时集合不变。
func (s *Set) Add(k, w int64) error {
	i := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].K >= k })
	if i < len(s.pts) && s.pts[i].K == k {
		return ErrDupSync
	}
	if i > 0 && s.pts[i-1].W >= w {
		return ErrSkew
	}
	if i < len(s.pts) && w >= s.pts[i].W {
		return ErrSkew
	}
	s.pts = append(s.pts, Point{})
	copy(s.pts[i+1:], s.pts[i:])
	s.pts[i] = Point{K: k, W: w}
	return nil
}

// Len 返回同步点数量。
func (s *Set) Len() int { return len(s.pts) }

// At 返回第 i 个（按 k 升序）同步点。
func (s *Set) At(i int) Point { return s.pts[i] }

// Last 返回最后一个同步点；空集合时 ok 为 false。
func (s *Set) Last() (Point, bool) {
	if len(s.pts) == 0 {
		return Point{}, false
	}
	return s.pts[len(s.pts)-1], true
}

// Span 定位 k：prev 为 k0<=k 的最大同步点（exact 表示恰有 k0==k），
// next 为 k1>=k 的最小同步点。不存在对应点时对应 ok 为 false。
func (s *Set) Span(k int64) (prev Point, prevOK bool, next Point, nextOK bool, exact bool) {
	i := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].K >= k })
	if i < len(s.pts) {
		next = s.pts[i]
		nextOK = true
		if next.K == k {
			return next, true, next, true, true
		}
	}
	if i > 0 {
		prev = s.pts[i-1]
		prevOK = true
	}
	return
}

// Interp 计算 p=(k0,W0) 与 n=(k1,W1) 之间 k 处的线性插值，向负无穷取整：
// W0 + floor((k-k0)*(W1-W0)/(k1-k0))。乘积经 big.Int 运算，任意合法输入不溢出。
func Interp(k int64, p, n Point) int64 {
	num := new(big.Int).Mul(big.NewInt(k-p.K), big.NewInt(n.W-p.W))
	q := num.Quo(num, big.NewInt(n.K-p.K))
	return p.W + q.Int64()
}
