package cluster

import "errors"

var (
	// ErrInvalidArgument 为构造参数或 Ingest 参数非法。
	ErrInvalidArgument = errors.New("cluster: invalid argument")
	// ErrTenantLimit 为租户数已达 Nt 时出现新租户。
	ErrTenantLimit = errors.New("cluster: tenant limit reached")
	// ErrNoSuchTenant 表示租户从未被成功 Ingest。
	ErrNoSuchTenant = errors.New("cluster: no such tenant")
)

// TemplateInfo 是对外暴露的模板只读视图。
type TemplateInfo struct {
	ID    int64
	Text  []string
	Count int64
	Gen   int64
}

// Result 是一次 Ingest 的结果。
type Result struct {
	// ID 为命中/新建模板的租户型 id；溢出时为 0。
	ID int64
	// Created 为真表示本次新建了模板。
	Created bool
	// Overflow 为真表示模板数已达上限且无合格模板，计入溢出桶。
	Overflow bool
	// Matched 为真表示命中既有模板（非新建、非溢出）。
	Matched bool
	// Compared 是本次在所属叶子中实际比较的模板数。
	Compared int
	// Eq 是命中模板（或最佳候选）的相等位置计数，便于测试与日志判定。
	Eq int
}

// leafKey 为叶子键：(词数 n, 首词)。
type leafKey struct {
	n     int
	first string
}

// tpl 是租户内一个模板的可变状态。
type tpl struct {
	id    int64
	words []string
	count int64
	gen   int64
}

// leaf 保存同一叶子键下按创建次序（id 升序）的模板。
type leaf struct {
	tpls []*tpl
}

// tenantState 是单个租户的全部状态。
type tenantState struct {
	leaves   map[leafKey]*leaf
	nextID   int64
	count    int // 模板总数（不含溢出桶）
	overflow int64
}
