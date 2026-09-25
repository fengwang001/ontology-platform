// Package eol 识别流式字节流中的行尾："\r\n"、单独的 "\r"、"\n"。
//
// 关键是 \r 的待定状态：读到 \r 时尚不知道下一字节是否为 \n，
// 因此把它悬置，由下一字节或流结束裁决。本包不依赖其他包，且无状态。
package eol

// Event 是单字节裁决结果。
type Event uint8

const (
	Keep    Event = iota // 普通字节原样输出（含 NUL、非法 UTF-8）
	Newline              // 该字节输出为行尾 \n（原生 \n，或单独 \r）
	DropCR               // 该 \r 是 "\r\n" 中的 \r，删除
)

// Decide 裁决一个紧随待定 \r 之后到达的字节 b。
// 返回 (对前一个待定 \r 的事件, 对 b 的事件)：
//   - b=='\n'：前 \r 删除(DropCR)，b 输出 \n(Newline)；
//   - b=='\r'：前 \r 单独成尾(Newline)，b 成为新的待定 \r（调用方按字节置位）；
//   - 其他：前 \r 单独成尾(Newline)，b 原样保留(Keep)。
func Decide(b byte) (prev, cur Event) {
	if b == '\n' {
		return DropCR, Newline
	}
	return Newline, Keep
}

// Flush 在流结束（Close）时裁决仍待定的 \r：它是单独行尾。
func Flush() Event { return Newline }
