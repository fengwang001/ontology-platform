package alias

// 下列访问器仅供同目录测试使用，用于读取 Action 的未导出字段。
func ActionKind(a Action) int         { return a.kind }
func ActionAliasName(a Action) string { return a.Alias }
func ActionMustExist(a Action) bool   { return a.mustExist }
