package rangespec

// Kind 标识一个区间的三种写法。
type Kind int

const (
	// FromTo 对应 "a-b"，含两端。
	FromTo Kind = iota
	// FromEnd 对应 "a-"，从 a 到资源末尾。
	FromEnd
	// Suffix 对应 "-n"，取末尾 n 字节。
	Suffix
)

// Spec 是解析后的单个区间规格，数值尚未结合资源长度归一化。
type Spec struct {
	Kind    Kind
	Start   int64 // FromTo / FromEnd 的起点
	End     int64 // FromTo 的终点（含）
	SuffixN int64 // Suffix 的 n
}
