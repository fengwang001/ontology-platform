// Package registry 维护可识别的快照格式版本目录：每个版本是否记录有效时间轴、
// 是否记录事务时间轴，以及各轴采用的区间端点闭合约定。
package registry

import "ontology/bitemporal"

// Format 描述一个可识别的快照格式版本。
type Format struct {
	Version      string
	RecordsValid bool
	RecordsTx    bool
	ValidBounds  bitemporal.BoundaryConvention
	TxBounds     bitemporal.BoundaryConvention
}

// RecordsAxis 返回该版本是否记录指定时间轴。
func (f Format) RecordsAxis(axis bitemporal.Axis) bool {
	switch axis {
	case bitemporal.ValidTime:
		return f.RecordsValid
	case bitemporal.TransactionTime:
		return f.RecordsTx
	default:
		return false
	}
}

// Convention 返回该版本对指定时间轴的端点闭合约定。
func (f Format) Convention(axis bitemporal.Axis) bitemporal.BoundaryConvention {
	switch axis {
	case bitemporal.ValidTime:
		return f.ValidBounds
	case bitemporal.TransactionTime:
		return f.TxBounds
	default:
		return bitemporal.BoundaryConvention{}
	}
}

// Registry 是只读的版本目录。目录内容在构造时固定，之后永不改变；
// 所有查询方法无副作用，可被任意并发调用。
type Registry struct {
	formats map[string]Format
}

// New 构造内置目录。四个基础版本恰好覆盖两条时间轴“记录与否”的全部四种组合：
//
//	V1 —— 只记录事务时间轴（旧版快照），事务轴使用 [) 半开约定；
//	V2 —— 同时记录有效与事务两条时间轴，两轴均使用 [) 半开约定；
//	V3 —— 同时记录两条时间轴，两轴均使用 [] 闭合约定（与 V2 端点语义不同）；
//	V4 —— 只记录有效时间轴，有效轴使用 [) 半开约定。
//
// 另外提供仅单轴闭合约定不同的变体版本，用于边界语义对照：
//
//	V2O —— 记录两条时间轴，两轴均使用 () 全开放约定；
//	V3V —— 记录两条时间轴，有效轴 []、事务轴 [)，隔离出单条轴的边界差异；
//	V5  —— 记录两条时间轴，两轴同样使用 [) 约定（边界语义与 V2 完全相同，
//	         代表仅序列化写法变化的版本，用于连续迁移往返一致性的等价对照）。
func New() *Registry {
	halfOpen := bitemporal.BoundaryConvention{StartClosed: bitemporal.Closed, EndClosed: bitemporal.Open}
	closed := bitemporal.BoundaryConvention{StartClosed: bitemporal.Closed, EndClosed: bitemporal.Closed}
	fullOpen := bitemporal.BoundaryConvention{StartClosed: bitemporal.Open, EndClosed: bitemporal.Open}

	defs := []Format{
		{Version: "V1", RecordsValid: false, RecordsTx: true, TxBounds: halfOpen},
		{Version: "V2", RecordsValid: true, RecordsTx: true, ValidBounds: halfOpen, TxBounds: halfOpen},
		{Version: "V3", RecordsValid: true, RecordsTx: true, ValidBounds: closed, TxBounds: closed},
		{Version: "V4", RecordsValid: true, RecordsTx: false, ValidBounds: halfOpen},
		{Version: "V2O", RecordsValid: true, RecordsTx: true, ValidBounds: fullOpen, TxBounds: fullOpen},
		{Version: "V3V", RecordsValid: true, RecordsTx: true, ValidBounds: closed, TxBounds: halfOpen},
		{Version: "V5", RecordsValid: true, RecordsTx: true, ValidBounds: halfOpen, TxBounds: halfOpen},
	}

	r := &Registry{formats: make(map[string]Format, len(defs))}
	for _, f := range defs {
		r.formats[f.Version] = f
	}
	return r
}

// Register 在副本上追加一个自定义版本，返回新目录（原目录不被修改）。
// 主要供测试构造额外迁移场景使用。
func (r *Registry) Register(f Format) *Registry {
	next := &Registry{formats: make(map[string]Format, len(r.formats)+1)}
	for k, v := range r.formats {
		next.formats[k] = v
	}
	next.formats[f.Version] = f
	return next
}

// Lookup 查询版本；ok 为 false 表示版本号超出可识别范围。
func (r *Registry) Lookup(version string) (Format, bool) {
	f, ok := r.formats[version]
	return f, ok
}
