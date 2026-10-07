package ontology

import "fmt"

// ErrorClass 是比对错误的类别。三类错误互不相同，判定顺序固定：
//
//  1. ErrClassCorrupt   快照本身结构性损坏，无法比对
//  2. ErrClassConflict  结构层差异判定冲突
//  3. ErrClassBasis     实例层因结构变化无法确定比对口径
//
// 顺序即依赖顺序：实例口径依赖结构差异，结构差异依赖快照完好，
// 因此按依赖序判定，先报出的永远是更根本的原因。
type ErrorClass string

const (
	ErrClassCorrupt  ErrorClass = "snapshot_corrupt"
	ErrClassConflict ErrorClass = "schema_diff_conflict"
	ErrClassBasis    ErrorClass = "instance_basis_undetermined"
)

// CompareError 是一次失败比对的结构化错误。
type CompareError struct {
	Class  ErrorClass `json:"class"`
	Detail string     `json:"detail"`
}

func (e *CompareError) Error() string {
	return fmt.Sprintf("compare failed (%s): %s", e.Class, e.Detail)
}

// ErrorClassOf 提取错误的类别；非比对错误返回空串。
func ErrorClassOf(err error) ErrorClass {
	if ce, ok := err.(*CompareError); ok {
		return ce.Class
	}
	return ""
}

// Stats 记录一次比对的工作量指标，用于复核"开销只与真实差异相关"。
type Stats struct {
	// Candidates 是目标快照中被检查的新增/修改实体数（对象+链接）。
	Candidates int `json:"candidates"`
	// Tombstones 是被检查的删除墓碑数。
	Tombstones int `json:"tombstones"`
	// BaseLookups 是在基准快照中进行的定点查询次数。
	BaseLookups int `json:"baseLookups"`
	// FullScan 为 true 表示走了跨谱系的全量扫描路径（O(n)）。
	FullScan bool `json:"fullScan"`
}

// Inspected 返回本次比对触及的实体相关工作量总量。
func (s Stats) Inspected() int {
	return s.Candidates + s.Tombstones + s.BaseLookups
}

// Result 是一次完整比对的输出：先结构层差异，再实例层差异。
type Result struct {
	Schema    *SchemaDiff   `json:"schema"`
	Instances *InstanceDiff `json:"instances"`
	Stats     Stats         `json:"stats"`
}

// Compare 比对两份快照：old 为基准（较早），new 为目标（较晚）。
//
// 管线顺序固定：快照校验 → 结构层差异 → 差异一致性校验 →
// 口径对齐 → 实例层差异。Compare 是纯函数，不修改输入快照，
// 不持有任何调用间状态，可安全并发调用。
//
// 同一谱系（Lineage 相同）时要求 old.Revision <= new.Revision，
// 并走修订号快速路径；不同谱系时退化为全量扫描（Stats.FullScan）。
func Compare(oldS, newS *Snapshot) (*Result, error) {
	if err := ValidateSnapshot(oldS); err != nil {
		return nil, &CompareError{Class: ErrClassCorrupt, Detail: "base snapshot: " + err.Error()}
	}
	if err := ValidateSnapshot(newS); err != nil {
		return nil, &CompareError{Class: ErrClassCorrupt, Detail: "target snapshot: " + err.Error()}
	}
	sameLineage := oldS.Lineage == newS.Lineage
	if sameLineage && oldS.Revision > newS.Revision {
		return nil, fmt.Errorf("compare: base revision %d is newer than target revision %d", oldS.Revision, newS.Revision)
	}
	sd := DiffSchema(oldS.Schema, newS.Schema)
	if err := sd.Validate(); err != nil {
		return nil, err
	}
	al, err := BuildAlignment(oldS.Schema, newS.Schema, sd)
	if err != nil {
		return nil, err
	}
	id, stats, err := diffInstances(oldS, newS, al, sameLineage)
	if err != nil {
		return nil, err
	}
	return &Result{Schema: sd, Instances: id, Stats: stats}, nil
}
