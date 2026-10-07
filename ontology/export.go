package ontology

// ExportResult 是一次已完成导出的不可变结果。
type ExportResult struct {
	ID         string
	TypeName   string
	Principal  string
	Included   []string          // 有权限、被导出的属性
	Exclusions map[string]string // 被排除属性 -> 命中规则版本 ID
	Seq        uint64            // 在全局串行顺序中的位置
}

// AuditRecord 是审计溯源查询的返回结果。
type AuditRecord struct {
	ExportID   string
	Property   string
	RuleID     string // 命中规则的标识
	Rule       RuleVersion
	DeclaredOn string // 规则直接声明所在的类型
}
