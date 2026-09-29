// Package lineage 实现数据血缘追踪：记录对象派生关系（输入→输出），
// 支持上游（谁产生我）与下游（我产生谁）双向追溯，并保证血缘指向当前版本。
package lineage

import "time"

// Kind 表示对象的产生方式。
type Kind string

const (
	// Source 表示源对象（直接登记，无上游）。
	Source Kind = "source"
	// Derived 表示派生对象（由一次派生操作产生）。
	Derived Kind = "derived"
)

// Ref 指向某个对象的一个具体版本。
type Ref struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Node 是血缘图中的一个对象版本节点。
type Node struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Kind      Kind      `json:"kind"`
	Operation string    `json:"operation"`
	Inputs    []Ref     `json:"inputs,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Edge 表示一条“输入版本 → 输出版本”的血缘记录。
type Edge struct {
	From   Ref    `json:"from"`
	To     Ref    `json:"to"`
	Op     string `json:"op"`
	Active bool   `json:"active"` // 端点版本是否仍为当前版本
}

// Direction 表示追溯方向。
type Direction string

const (
	// Upstream 向上游追溯：谁产生了我。
	Upstream Direction = "upstream"
	// Downstream 向下游追溯：我产生了谁。
	Downstream Direction = "downstream"
	// Both 同时追溯上游与下游，得到以目标为中心的完整血缘子图。
	Both Direction = "both"
)

// Graph 是规范化（确定性排序）后的血缘图。
type Graph struct {
	Root  Ref    `json:"root"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// DeriveRequest 描述一次派生操作。
type DeriveRequest struct {
	OutputID  string // 新（或更新）对象的 ID
	Operation string // 派生操作类型
	Inputs    []Ref  // 输入对象版本
	Payload   string // 输出内容，参与版本指纹
}
