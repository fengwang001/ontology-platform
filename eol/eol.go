// Package eol 识别字节级行尾记号：CRLF、独立 CR 与 LF。
// 它不依赖其他包，也不保存状态；跨切分点的 CR 由调用方按
// PendingCR 语义暂存。
package eol

// Kind 是字节在行尾识别中的分类。
type Kind int

const (
	// Other 表示与行尾无关的普通字节。
	Other Kind = iota
	// CR 表示回车；只有看到下一字节才能区分独立 CR 与 CRLF。
	CR
	// LF 表示换行。
	LF
)

// Classify 返回字节 b 的行尾分类。
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// IsEOL 报告 b 是否参与行尾（CR 或 LF）。
func IsEOL(b byte) bool { return Classify(b) != Other }

// PendingCR 描述一个已读入但尚未判定的 CR 在看到下一字节时的结论。
type PendingCR int

const (
	// LoneCR 表示下一字节不是 LF（或流结束），CR 是独立行尾。
	LoneCR PendingCR = iota
	// CRLF 表示下一字节是 LF，两者构成一个行尾。
	CRLF
)

// Resolve 判定待定 CR 在下一字节 next 之前的归属；eof 为真时
// 表示流结束，CR 必为独立行尾。
func Resolve(next byte, eof bool) PendingCR {
	if !eof && next == '\n' {
		return CRLF
	}
	return LoneCR
}
