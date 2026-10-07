// Package bitemporal 实现本体平台上的跨链接双时态溯源查询子系统。
//
// 双时态：
//   - 有效时间（valid time）：事实在业务世界中成立的时间，表现为左闭右开区间 [From, To)
//   - 写入时间（transaction/write time）：记录进入系统的时间，只追加、不修改
package bitemporal

import "time"

// Interval 是左闭右开的有效时间区间 [From, To)。
type Interval struct {
	From time.Time
	To   time.Time
}

// ObjectRecord 是对象实例的一条写入记录（一个版本）。
// 同一 ID 的对象可拥有多条记录，每条记录携带完整区间与独立写入时间；
// 后续修正以追加新记录方式保存，旧记录永不覆盖或删除。
type ObjectRecord struct {
	ID        string
	VersionID string
	Valid     Interval
	WrittenAt time.Time
}

// LinkRecord 是链接实例的一条写入记录（一个版本）。
// 链接端点（SourceID/TargetID）属于链接身份，同一链接各版本端点必须一致。
type LinkRecord struct {
	ID        string
	VersionID string
	SourceID  string
	TargetID  string
	Valid     Interval
	WrittenAt time.Time
}

// Status 是单个参与方（源对象 / 链接 / 目标对象）在给定双时态点上的可见性类别。
type Status int

const (
	// StatusVisible 表示该方在 (validAt, asOf) 下可见。
	StatusVisible Status = iota
	// StatusNotEstablished 表示在有效时间点 validAt 上不存在任何覆盖记录
	// （无论写入时间多晚），即「此刻未建立」。
	StatusNotEstablished
	// StatusNotYetVisible 表示存在覆盖 validAt 的记录，但截至写入时间点 asOf
	// 已落定的最新记录并不覆盖，即「已建立但对此查询尚不可见」。
	StatusNotYetVisible
)

// HopEvidence 记录一条路径中某一跳据以判定可见的三方版本标识。
type HopEvidence struct {
	LinkID          string
	LinkVersionID   string
	SourceID        string
	SourceVersionID string
	TargetID        string
	TargetVersionID string
}

// Path 是一条通过校验的溯源路径。
type Path struct {
	// Nodes 为路径节点序列，Nodes[0] 为源对象，长度 = 跳数 + 1。
	Nodes []string
	// Links 为每一跳经过的链接标识，长度 = 跳数。
	Links []string
	// Evidence 为每一跳三方可见记录标识，长度 = 跳数。
	Evidence []HopEvidence
}

// BlockedEdge 描述在遍历边界上未通过三方校验的一跳，供调用方区分不可见类别。
type BlockedEdge struct {
	Prefix       []string
	LinkID       string
	TargetID     string
	SourceStatus Status
	LinkStatus   Status
	TargetStatus Status
	// Combined 为该跳综合状态：任一方 NotEstablished 即为 NotEstablished，
	// 否则（至少一方 NotYetVisible）为 NotYetVisible。
	Combined Status
}

// Query 是一次 AsOf 溯源查询的输入。
type Query struct {
	SourceID string
	ValidAt  time.Time
	AsOf     time.Time
	MaxDepth int
}

// QueryStats 是一次查询实际触及的候选规模计数，用于有界性核对。
type QueryStats struct {
	ObjectsResolved int
	LinksConsidered int
	HopsChecked     int
}

// Result 是一次 AsOf 溯源查询的结果。
type Result struct {
	Paths   []Path
	Blocked []BlockedEdge
	Stats   QueryStats
}
