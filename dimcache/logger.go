package dimcache

// Logger 接收每一步判定的结构化日志。nil 表示静默。
type Logger interface {
	Log(step string, fields map[string]any)
}
