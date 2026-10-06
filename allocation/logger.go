package allocation

// Tracer 操作审计追踪：测试中用于打印每步输入、输出与判定依据。
type Tracer interface {
	Logf(format string, args ...any)
}
