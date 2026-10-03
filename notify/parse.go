package notify

import "strings"

type seg struct {
	lit    string
	isVar  bool
	name   string
	hasDef bool
	def    string
}

func syntaxError(off int) *Error {
	return &Error{Kind: KindSyntax, Offset: off, detail: "syntax error in template body"}
}

// parseBody 自左向右扫描正文，{{ 与 }} 优先按字面处理。
func parseBody(body string) ([]seg, *Error) {
	var segs []seg
	for i := 0; i < len(body); {
		switch body[i] {
		case '{':
			if i+1 < len(body) && body[i+1] == '{' {
				segs = append(segs, seg{lit: "{"})
				i += 2
				continue
			}
			// 解析 {v} 或 {v|text}，以紧随的第一个 } 结束。
			j := i + 1
			nameStart := j
			for j < len(body) {
				c := body[j]
				if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
					j++
					continue
				}
				break
			}
			name := body[nameStart:j]
			if name == "" || j >= len(body) {
				return nil, syntaxError(i)
			}
			switch body[j] {
			case '}':
				segs = append(segs, seg{isVar: true, name: name})
				i = j + 1
			case '|':
				k := j + 1
				textStart := k
				for k < len(body) && body[k] != '}' && body[k] != '{' && body[k] != '|' {
					k++
				}
				if k >= len(body) || body[k] != '}' {
					return nil, syntaxError(i)
				}
				segs = append(segs, seg{isVar: true, name: name, hasDef: true, def: body[textStart:k]})
				i = k + 1
			default:
				return nil, syntaxError(i)
			}
		case '}':
			if i+1 < len(body) && body[i+1] == '}' {
				segs = append(segs, seg{lit: "}"})
				i += 2
				continue
			}
			return nil, syntaxError(i)
		default:
			next := strings.IndexAny(body[i:], "{}")
			if next < 0 {
				segs = append(segs, seg{lit: body[i:]})
				return segs, nil
			}
			segs = append(segs, seg{lit: body[i : i+next]})
			i += next
		}
	}
	return segs, nil
}
