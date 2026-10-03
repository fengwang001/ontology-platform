package notify

// segKind 区分字面文本与变量片段。
type segKind int

const (
	segLiteral segKind = iota
	segVar
)

type segment struct {
	kind segKind
	// literal: 字面文本（{{ 与 }} 已折叠为单个符号）；
	// var: 变量名。
	text string
	// hasDefault 仅对 var 有效，表示语法中是否带 |text。
	hasDefault bool
	def        string
}

type parsedBody struct {
	segments []segment
}

// parseBody 解析正文语法，返回首个语法错误的字节偏移：
// {{ 与 }} 为字面符号；{v} 与 {v|text} 为变量；
// 其余单个 { 或 } 为语法错误。变量解析以紧随的第一个 } 结束，
// 因此 {a}} 被识别为变量 a 后随单个 }（偏移 3）。
func parseBody(body string) (*parsedBody, *Error) {
	pb := &parsedBody{}
	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			pb.segments = append(pb.segments, segment{kind: segLiteral, text: string(lit)})
			lit = lit[:0]
		}
	}
	i := 0
	for i < len(body) {
		switch body[i] {
		case '{':
			if i+1 < len(body) && body[i+1] == '{' {
				lit = append(lit, '{')
				i += 2
				continue
			}
			end := indexByte(body, '}', i+1)
			if end < 0 {
				return nil, &Error{Reason: ReasonSyntax, Offset: i}
			}
			inner := body[i+1 : end]
			name := inner
			hasDefault := false
			def := ""
			if bar := indexByte(inner, '|', 0); bar >= 0 {
				name = inner[:bar]
				def = inner[bar+1:]
				hasDefault = true
			}
			if !validVariable(name) {
				return nil, &Error{Reason: ReasonSyntax, Offset: i}
			}
			flush()
			pb.segments = append(pb.segments, segment{
				kind: segKind(segVar), text: name, hasDefault: hasDefault, def: def,
			})
			i = end + 1
		case '}':
			if i+1 < len(body) && body[i+1] == '}' {
				lit = append(lit, '}')
				i += 2
				continue
			}
			return nil, &Error{Reason: ReasonSyntax, Offset: i}
		default:
			lit = append(lit, body[i])
			i++
		}
	}
	flush()
	return pb, nil
}

func indexByte(s string, c byte, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// render 按变量映射生成文本；缺失变量按名字升序返回。
// 请求渠道为 email 时，仅对变量值做 HTML 转义；
// 默认文本与正文字面文本不转义。
func (pb *parsedBody) render(vars map[string]string, requestChannel string) (string, []string) {
	var missing []string
	for _, seg := range pb.segments {
		if seg.kind != segVar {
			continue
		}
		if _, ok := vars[seg.text]; !ok && !seg.hasDefault {
			missing = append(missing, seg.text)
		}
	}
	if len(missing) > 0 {
		sortStrings(missing)
		return "", missing
	}
	var out []byte
	for _, seg := range pb.segments {
		switch seg.kind {
		case segLiteral:
			out = append(out, seg.text...)
		case segVar:
			val, ok := vars[seg.text]
			if !ok {
				out = append(out, seg.def...)
				continue
			}
			if requestChannel == "email" {
				out = appendEscaped(out, val)
			} else {
				out = append(out, val...)
			}
		}
	}
	return string(out), nil
}

func appendEscaped(dst []byte, val string) []byte {
	for i := 0; i < len(val); i++ {
		switch val[i] {
		case '&':
			dst = append(dst, "&amp;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '"':
			dst = append(dst, "&quot;"...)
		default:
			dst = append(dst, val[i])
		}
	}
	return dst
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
