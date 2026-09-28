package txnset

import (
	"math"
	"unicode"
	"unicode/utf8"
)

// MaxIntervals 限制单次解析文本中允许出现的区间条目总数。
const MaxIntervals = 10000

// Parse 严格解析一段事务集合文本，成功时返回一个全新的、已规范化的
// *Set。解析是纯函数：不接触任何既有集合状态。
//
// 语法（任意位置都不允许空白）：
//
//	文本     = 条目 (';' 条目)*
//	条目     = 来源 ':' 区间 (',' 区间)*
//	来源     = 字母 (字母 | 数字 | '_' | '-')*
//	区间     = 号 | 号 '-' 号      // 后者为闭区间，要求 lo <= hi
//	号       = '0' | 非零数字 数字*  // 禁止前导零，范围 [0, 2^64-1]
//
// 任一位置非法即返回 *ParseError（从左到右第一个错误）；整段文本
// 全部合法后才会产出集合，因此“前段合法、后段非法”不会产生任何部分结果。
func Parse(text string) (*Set, error) {
	p := &parser{text: text}
	parsed := make(map[string][]Interval)
	total := 0

	for p.pos < len(p.text) {
		src, err := p.parseSource()
		if err != nil {
			return nil, err
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		for {
			iv, err := p.parseInterval()
			if err != nil {
				return nil, err
			}
			total++
			if total > MaxIntervals {
				return nil, parseErrorAt(KindTooMany, p.intervalStart,
					"interval count exceeds limit %d", MaxIntervals)
			}
			parsed[src] = append(parsed[src], iv)
			if p.peek() != ',' {
				break
			}
			p.pos++ // 消费 ','
		}
		if p.pos == len(p.text) {
			break
		}
		if err := p.expect(';'); err != nil {
			return nil, err
		}
		if p.pos == len(p.text) {
			return nil, parseErrorAt(KindSyntax, p.pos, "trailing ';' without entry")
		}
	}

	out := New()
	for src, ivs := range parsed {
		out.sources[src] = mergeIntervals(ivs)
	}
	logf("parse input=%q -> %s | decision: %s", text, out.Canonical(), "all entries legal, normalized by source")
	return out, nil
}

// parser 是文本解析器，pos 为当前字节偏移。
type parser struct {
	text          string
	pos           int
	intervalStart int // 最近一个区间的起始偏移，用于超限报错定位
}

// peek 返回当前字节；已到末尾返回 0。
func (p *parser) peek() byte {
	if p.pos >= len(p.text) {
		return 0
	}
	return p.text[p.pos]
}

// expect 要求当前位置为 c，否则在该位置报语法错误。
func (p *parser) expect(c byte) error {
	if p.pos >= len(p.text) || p.text[p.pos] != c {
		want := "end of input"
		if p.pos < len(p.text) {
			want = quoteByte(p.text[p.pos])
		}
		return parseErrorAt(KindSyntax, p.pos, "expected %q but found %s", c, want)
	}
	p.pos++
	return nil
}

// rejectSpace 若当前字符是空白则返回语法错误（按 rune 判定，覆盖制表符等）。
func (p *parser) rejectSpace() error {
	if p.pos >= len(p.text) {
		return nil
	}
	r, _ := utf8.DecodeRuneInString(p.text[p.pos:])
	if unicode.IsSpace(r) {
		return parseErrorAt(KindSyntax, p.pos, "whitespace is not allowed")
	}
	return nil
}

// parseSource 解析来源标识。
func (p *parser) parseSource() (string, error) {
	if err := p.rejectSpace(); err != nil {
		return "", err
	}
	start := p.pos
	c := p.peek()
	if !isAlpha(c) {
		if c == 0 {
			return "", parseErrorAt(KindSyntax, start, "expected source identifier but found end of input")
		}
		if isDelimiter(c) {
			return "", parseErrorAt(KindSyntax, start, "expected source identifier but found %s", quoteByte(c))
		}
		return "", parseErrorAt(KindIdentifier, start, "source identifier must start with a letter but found %s", quoteByte(c))
	}
	p.pos++
	for p.pos < len(p.text) {
		if err := p.rejectSpace(); err != nil {
			return "", err
		}
		c := p.text[p.pos]
		if c == ':' {
			return p.text[start:p.pos], nil
		}
		if !isIdentChar(c) {
			return "", parseErrorAt(KindIdentifier, p.pos,
				"invalid character %s in source identifier", quoteByte(c))
		}
		p.pos++
	}
	return "", parseErrorAt(KindSyntax, p.pos, "unterminated source identifier, missing ':'")
}

// parseInterval 解析单点 n 或闭区间 lo-hi。
func (p *parser) parseInterval() (Interval, error) {
	p.intervalStart = p.pos
	lo, err := p.parseNumber()
	if err != nil {
		return Interval{}, err
	}
	if p.peek() != '-' {
		return Interval{Lo: lo, Hi: lo}, nil
	}
	dashPos := p.pos
	p.pos++ // 消费 '-'
	hi, err := p.parseNumber()
	if err != nil {
		return Interval{}, err
	}
	if hi < lo {
		return Interval{}, parseErrorAt(KindNumber, dashPos,
			"interval lower bound %d is greater than upper bound %d", lo, hi)
	}
	return Interval{Lo: lo, Hi: hi}, nil
}

// parseNumber 解析无符号十进制整数：拒绝前导零与 uint64 溢出。
func (p *parser) parseNumber() (uint64, error) {
	start := p.pos
	if err := p.rejectSpace(); err != nil {
		return 0, err
	}
	c := p.peek()
	if !isDigit(c) {
		if c == 0 {
			return 0, parseErrorAt(KindSyntax, start, "expected number but found end of input")
		}
		return 0, parseErrorAt(KindSyntax, start, "expected number but found %s", quoteByte(c))
	}
	if c == '0' {
		p.pos++
		// 前导零：'0' 后仍紧跟数字即非法，定位在该数字记号的起点。
		if p.pos < len(p.text) && isDigit(p.text[p.pos]) {
			return 0, parseErrorAt(KindNumber, start, "leading zeros are not allowed")
		}
		return 0, nil
	}

	var n uint64
	const max = math.MaxUint64
	for p.pos < len(p.text) && isDigit(p.text[p.pos]) {
		d := uint64(p.text[p.pos] - '0')
		if n > (max-d)/10 {
			return 0, parseErrorAt(KindNumber, start, "number overflows uint64")
		}
		n = n*10 + d
		p.pos++
	}
	return n, nil
}

// isDigit 报告 c 是否为 ASCII 数字。
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isAlpha 报告 c 是否为 ASCII 字母。
func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentChar 报告 c 是否为来源标识允许的字符。
func isIdentChar(c byte) bool {
	return isAlpha(c) || isDigit(c) || c == '_' || c == '-'
}

// isDelimiter 报告 c 是否为语法分隔符；分隔符出现在要求标识的位置
// 属于语法错误（而非标识内容非法）。
func isDelimiter(c byte) bool {
	return c == ':' || c == ';' || c == ',' || c == '-'
}

// quoteByte 以单引号形式渲染字节用于错误信息，不可打印时给出转义描述。
func quoteByte(c byte) string {
	switch c {
	case 0:
		return "end of input"
	case '\t':
		return `'\t'`
	case '\n':
		return `'\n'`
	}
	if c < 0x20 || c >= 0x7f {
		return "'\\x" + hexByte(c) + "'"
	}
	return "'" + string(c) + "'"
}

// hexByte 返回字节的两位小写十六进制表示。
func hexByte(c byte) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[c>>4], hexd[c&0xf]})
}
