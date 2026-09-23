package scalar

// Rune 是一个 Unicode 码点（未做合法性保证前的原始值）。
type Rune = rune

const (
	MaxRune     rune = 0x10FFFF
	SurrogateLo rune = 0xD800
	SurrogateHi rune = 0xDFFF
	Replacement rune = 0xFFFD
)

// Valid 报告码点是否为合法 Unicode 标量值（排除代理区与越界值）。
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告码点是否落入代理区 D800..DFFF。
func IsSurrogate(r rune) bool { return SurrogateLo <= r && r <= SurrogateHi }
