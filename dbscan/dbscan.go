// Package dbscan 实现滑动窗口增量 DBSCAN 聚类服务。
package dbscan

import "sync"

// Service 在二维整数点随插入、删除与时间推进过期不断变化时，
// 增量维护每个点的核心/边界/噪声身份与确定的簇标签。
type Service struct {
	mu sync.Mutex

	eps    int64
	minPts int
	w      int64
	c      int

	now int64

	points map[int64]*Point
	adj    map[int64]map[int64]bool
	core   map[int64]bool
	label  map[int64]int64
	grid   map[[2]int64]map[int64]bool

	heap    []int64
	heapPos map[int64]int

	// rangeQueries 是非导出计数器，度量发出的 eps 邻域查询次数。
	rangeQueries int64
}
