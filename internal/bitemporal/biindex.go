// Package bitemporal 维护每个主键的双时态索引。
//
// 业务时间是半开区间 [start, nextStart)：每个业务时间起点对应一条“按系统时间
// 演进的覆盖链”，起点更大的链隐式截断前一条链的业务终点。索引只负责定位，
// 不解释负载内容，也不感知逻辑删除（删除是提交层面的标记）。
package bitemporal

// Commit 是一次成功写入在双时态平面上的不可变投影。
type Commit struct {
	SysVersion int64 // 系统时间版本号（链内严格递增，从 1 开始）
	BizStart   int64 // 业务时间起点
}

// Index 是单个主键的双时态索引快照。零值即空索引，可直接使用。
type Index struct {
	root *node
}

// Apply 把一次提交并入索引，返回新索引；接收方与入参均不被修改。
func (idx Index) Apply(c Commit) Index {
	return Index{root: insert(idx.root, c)}
}

// AsOf 返回在系统时间 asOfSys 下、覆盖业务时间点 bizAt 的提交的系统版本号；
// ok 为 false 表示不存在满足条件的版本。
func (idx Index) AsOf(asOfSys, bizAt int64) (sysVersion int64, ok bool) {
	return idx.LastAsOf(asOfSys, bizAt, nil)
}

// Segment 表示系统时间 asOfSys 时刻可见的一个业务时间段。
type Segment struct {
	Start      int64
	End        int64
	SysVersion int64 // 该区间在 asOfSys 下可见的提交系统版本号
}

// OpenEnd 表示业务时间区间终点开放。
const OpenEnd int64 = 1<<63 - 1

// HistoryAsOf 返回系统时间 asOfSys 时刻可见的全部业务时间段，
// 按业务时间起点升序，end 为开放终点（无后继时为 OpenEnd）。
func (idx Index) HistoryAsOf(asOfSys int64) []Segment {
	var nodes []*node
	inorderAsc(idx.root, &nodes)
	segs := make([]Segment, 0, len(nodes))
	for i, n := range nodes {
		sv, ok := chainAt(n.chain, asOfSys, nil)
		if !ok {
			continue // 该起点在查询系统时间尚不存在
		}
		end := OpenEnd
		// 终点由“在 asOfSys 下已存在的下一个起点”隐式确定。
		for j := i + 1; j < len(nodes); j++ {
			if nodes[j].chain[0] <= asOfSys {
				end = nodes[j].key
				break
			}
		}
		segs = append(segs, Segment{Start: n.key, End: end, SysVersion: sv})
	}
	return segs
}

// LastAsOf 与 AsOf 相同，但把遍历访问的节点数写入 visits（nil 可忽略）。
func (idx Index) LastAsOf(asOfSys, bizAt int64, visits *int) (sysVersion int64, ok bool) {
	if visits != nil {
		*visits = 0
	}
	return lookupAt(idx.root, asOfSys, bizAt, visits)
}
