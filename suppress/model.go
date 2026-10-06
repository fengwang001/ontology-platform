// Package suppress 实现静态检查工具的抑制指令处理器。
//
// 它接收检查器产出的诊断（Diagnostic）与源文件中登记的抑制指令（Directive），
// 在某个一致快照上判定每条诊断是否被某条有效指令抑制，同时报告指令自身的问题
// （缺理由、无目标行、未知规则、重复禁用、孤立启用、未使用、未闭合区间）。
package suppress

// AllTag 是特殊规则标签：对任意规则的诊断一视同仁地生效，
// 它是独立标签，不会被展开为各具体规则标签。
const AllTag = "全部"

// Kind 为指令种类。
type Kind int

const (
	// KindThisLine 本行指令：作用于指令所在行。
	KindThisLine Kind = iota
	// KindNextLine 下一行指令：作用于所在行的下一行。
	KindNextLine
	// KindDisable 禁用指令：从所在行（含）起关闭标签，直到同标签启用指令所在行之前（不含）。
	KindDisable
	// KindEnable 启用指令：使其标签自所在行起不再被禁用。
	KindEnable
	// KindWholeFile 全文件指令：对其标签在全部行生效，与所在位置无关。
	KindWholeFile
)

// ParseKind 按规范名称解析指令种类，未知种类返回 ok=false。
func ParseKind(s string) (Kind, bool) {
	switch s {
	case "THIS_LINE":
		return KindThisLine, true
	case "NEXT_LINE":
		return KindNextLine, true
	case "DISABLE":
		return KindDisable, true
	case "ENABLE":
		return KindEnable, true
	case "WHOLE_FILE":
		return KindWholeFile, true
	default:
		return 0, false
	}
}

// String 返回指令种类的规范名称。
func (k Kind) String() string {
	switch k {
	case KindThisLine:
		return "THIS_LINE"
	case KindNextLine:
		return "NEXT_LINE"
	case KindDisable:
		return "DISABLE"
	case KindEnable:
		return "ENABLE"
	case KindWholeFile:
		return "WHOLE_FILE"
	default:
		return "UNKNOWN"
	}
}

// Diagnostic 是检查器产出的一条诊断。行号从 1 起且不超过文件总行数，列号从 1 起。
type Diagnostic struct {
	Line   int
	Column int
	Rule   string
}

// Directive 是源文件中的一条抑制指令。
type Directive struct {
	// Line 为指令所在行，从 1 起且不超过文件总行数。
	Line int
	// Kind 为指令种类。
	Kind Kind
	// Labels 为规则标签列表（按源文件中出现的次序）。空列表等同于只含“全部”。
	// 重复标签在登记时去重（只保留首次出现）。
	Labels []string
	// Reason 为理由文本（允许空白）。是否强制要求理由由会话配置决定。
	Reason string
}

// RegErrorCode 为登记阶段的错误类别。
type RegErrorCode int

const (
	// ErrInvalidParameter 参数非法：行号越界、列号小于 1、规则名为空、指令种类未知。
	ErrInvalidParameter RegErrorCode = iota
	// ErrDuplicate 重复登记：与某条已接受指令完全相同
	// （行号、种类、标签集合、理由均相同）。
	ErrDuplicate
)

// RegError 描述一次被拒绝的登记。
type RegError struct {
	Code RegErrorCode
	// Detail 为人可读的判定依据。
	Detail string
}

// IssueCode 为指令自身问题的类别。
type IssueCode int

const (
	// IssueMissingReason 缺理由：要求理由时理由为空或全空白；
	// 整条指令的所有标签都不生效（不开启/关闭任何区间，不抑制任何诊断）。
	IssueMissingReason IssueCode = iota
	// IssueNoTargetLine 无目标行：下一行指令位于最后一行，不生效。
	IssueNoTargetLine
	// IssueUnknownRule 未知规则：标签既非已知规则也非“全部”，该标签被忽略。
	IssueUnknownRule
	// IssueRepeatedDisable 重复禁用：同一标签已有未闭合禁用时再次禁用，第二条不生效。
	IssueRepeatedDisable
	// IssueOrphanEnable 孤立启用：对当前未被禁用的标签发出启用，不生效。
	IssueOrphanEnable
	// IssueUnused 未使用：本应生效的标签在其作用范围内没有抑制任何诊断。
	IssueUnused
	// IssueUnclosedRange 未闭合区间：禁用指令到文件末尾仍未被启用。
	// 指令级问题，与该指令各标签级问题并存，排在它们之前。
	IssueUnclosedRange
)

// String 返回问题类别的规范名称。
func (c IssueCode) String() string {
	switch c {
	case IssueMissingReason:
		return "MISSING_REASON"
	case IssueNoTargetLine:
		return "NO_TARGET_LINE"
	case IssueUnknownRule:
		return "UNKNOWN_RULE"
	case IssueRepeatedDisable:
		return "REPEATED_DISABLE"
	case IssueOrphanEnable:
		return "ORPHAN_ENABLE"
	case IssueUnused:
		return "UNUSED"
	case IssueUnclosedRange:
		return "UNCLOSED_RANGE"
	default:
		return "UNKNOWN"
	}
}

// priority 为同一指令同一标签最多一条问题时的优先级（小者优先）。
func (c IssueCode) priority() int { return int(c) }

// Issue 是一条指令自身的问题。
type Issue struct {
	// DirectiveLine 为问题所属指令的行号。
	DirectiveLine int
	// Label 为问题标签；指令级问题（未闭合区间）使用空字符串。
	Label string
	// Code 为问题类别。
	Code IssueCode
	// DirectiveOrder 为该指令的登记次序（0 起），用于稳定输出。
	DirectiveOrder int
}

// Attribution 指明一条被抑制诊断的归属：使其被抑制的（指令，标签）。
type Attribution struct {
	// DirectiveOrder 为归属指令的登记次序（0 起）。
	DirectiveOrder int
	// DirectiveLine 为归属指令所在行。
	DirectiveLine int
	// Kind 为归属指令种类。
	Kind Kind
	// Label 为命中的标签（具体规则名或“全部”）。
	Label string
}

// SuppressedDiagnostic 是一条被抑制诊断及其归属。
type SuppressedDiagnostic struct {
	Diagnostic  Diagnostic
	Attribution Attribution
	// Index 为该诊断在已接受诊断登记序列中的原始下标，稳定排序用。
	Index int
}

// KeptDiagnostic 保留一条未被抑制的诊断及其原始登记下标。
type KeptDiagnostic struct {
	Diagnostic Diagnostic
	Index      int
}

// Judgment 是一次判定的完整结果（只读快照上的一致视图）。
type Judgment struct {
	// Kept 为保留的诊断，按（行，列，规则名）升序；
	// 完全相同的诊断全部保留且相对登记次序不变。
	Kept []KeptDiagnostic
	// Suppressed 为被抑制的诊断，按同样次序排序，并带归属。
	Suppressed []SuppressedDiagnostic
	// Issues 为指令问题，按（指令行号，标签字典序，类别优先级）升序，
	// 指令级问题排在该指令的标签级问题之前。
	Issues []Issue
}
