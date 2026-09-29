package creditflow

// Logger 记录每一步的输入、信用、积压、缓冲与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}
