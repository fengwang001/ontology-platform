package sampler

// Element 是带权元素流中的一个元素。
type Element struct {
	ID     string
	Weight float64
}

// Selected 描述最终入选的一个元素及其计算出的键值。
type Selected struct {
	Element Element
	Key     float64
	Order   int // 元素到达顺序（从 0 开始），用于键值并列时先到者优先
}

// Source 是由调用方注入的随机数源。
// Next 返回区间 (0,1) 内的随机数；随机数用尽时必须返回错误。
type Source interface {
	Next() (float64, error)
}

// Logger 记录每一步的输入、键值与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}
