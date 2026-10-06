package suppress

type DirectiveKind string

const (
	KindLine     DirectiveKind = "line"
	KindNextLine DirectiveKind = "next_line"
	KindDisable  DirectiveKind = "disable"
	KindEnable   DirectiveKind = "enable"
	KindFile     DirectiveKind = "file"
)

const AllRules = "全部"

type IssueCategory string

const (
	IssueMissingReason    IssueCategory = "缺理由"
	IssueNoTargetLine     IssueCategory = "无目标行"
	IssueUnknownRule      IssueCategory = "未知规则"
	IssueRedundantDisable IssueCategory = "重复禁用"
	IssueOrphanEnable     IssueCategory = "孤立启用"
	IssueUnused           IssueCategory = "未使用"
	IssueUnclosedRange    IssueCategory = "未闭合区间"
)

type Diagnostic struct {
	Line   int
	Column int
	Rule   string
}

type Directive struct {
	Line   int
	Kind   DirectiveKind
	Tags   []string
	Reason string
}

type SuppressedDiagnostic struct {
	Diagnostic
	DirectiveLine int
	Tag           string
}

type DirectiveIssue struct {
	DirectiveLine    int
	Tag              string
	Category         IssueCategory
	InstructionLevel bool
}

type Report struct {
	Kept       []Diagnostic
	Suppressed []SuppressedDiagnostic
	Issues     []DirectiveIssue
}

type suppressorIdentity int

type reportedIssueKey struct {
	sequence int
	kind     DirectiveKind
	tag      string
	category IssueCategory
}
