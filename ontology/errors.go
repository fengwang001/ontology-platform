package ontology

import "fmt"

// ErrorClass 审计错误类别。一次审计请求同时具备多类错误条件时，
// 按下列声明顺序（数值小者优先）只报告其中一类：
//
//  1. ErrorContradictoryInterval —— 请求的记录时刻区间自相矛盾；
//  2. ErrorObjectTypeMissing    —— 链接两端对象类型任一方在请求的记录时刻尚不存在；
//  3. ErrorVersionSuperseded    —— 审计所依据的基数约束版本在请求处理期间已被作废；
//  4. ErrorMirrorInconsistency  —— 回放发现镜像一致性结构性缺失（而非单纯基数超限）。
type ErrorClass int

const (
	ErrorNone ErrorClass = iota
	ErrorContradictoryInterval
	ErrorObjectTypeMissing
	ErrorVersionSuperseded
	ErrorMirrorInconsistency
)

func (c ErrorClass) String() string {
	switch c {
	case ErrorContradictoryInterval:
		return "contradictory-interval"
	case ErrorObjectTypeMissing:
		return "object-type-missing"
	case ErrorVersionSuperseded:
		return "constraint-version-superseded"
	case ErrorMirrorInconsistency:
		return "mirror-inconsistency"
	}
	return "none"
}

// AuditError 审计错误的结构化表示。审计错误只影响本次审计输出，
// 不会对链接历史轨迹产生任何可观察的改动。
type AuditError struct {
	Class  ErrorClass `json:"class"`
	Detail string     `json:"detail"`
}

func (e *AuditError) Error() string {
	return fmt.Sprintf("audit error %s: %s", e.Class, e.Detail)
}
