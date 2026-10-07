package ontology

// 本文件定义索引生命周期内可区分的错误类别。
//
// 拒绝优先级（针对查询请求）由调用方依据下列错误类别按固定顺序判定：
//
//  1. ErrIndexNotDeclared  对象类型未声明该索引（最高优先级）
//  2. ErrIndexUnavailable  索引正在重建 / 上次重建中途失败（不可用）
//  3. ErrIndexInconsistent 索引可用，但复核曾发现且尚未修复的条目级不一致
//     （查询不失败，但结果中携带不一致声明）
//  4. EntryMismatch        复核发现的条目级不一致（体现在复核报告中）

import "errors"

// ErrIndexNotDeclared 对象类型未声明该索引。
var ErrIndexNotDeclared = errors.New("index not declared on object type")

// ErrIndexUnavailable 索引尚不存在完整可用的审计记录（重建中或中途失败）。
var ErrIndexUnavailable = errors.New(
	"index unavailable: rebuild in progress or aborted without complete audit record")

// ErrIndexInconsistent 索引可用，但复核曾发现且尚未修复的条目级不一致。
//
// 该错误不单独作为查询失败返回：查询仍返回结果，并在结果中携带不一致声明。
var ErrIndexInconsistent = errors.New(
	"index available but flagged inconsistent pending repair")

// 条目级不一致（复核发现）由 verify.go 的 Mismatch 结构承载，其 Kind 字段
// 可区分 "entry_value_mismatch" / "entry_object_missing" /
// "entry_duplicate" / "entry_missing" 四种情形，并随复核报告逐条返回，
// 因此这里不再设置独立的错误类型——查询不因其失败，仅需在报告中呈现。
