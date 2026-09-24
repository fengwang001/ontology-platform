// Package logical 把 properties 文本的物理行拼成逻辑行：
// 跳过注释行与空行，处理行尾奇数个反斜杠的续行，
// 并剥掉每条物理行（含续行）首部的空白。
package logical

// checked 是非导出计数器，记录字节被检查的总次数。
var checked int64

// Checked 返回至今检查过的字节总数。
func Checked() int64 { return checked }

// Reset 把字节检查计数器清零。
func Reset() { checked = 0 }

// Seg 记录逻辑行的一段文本来自哪条物理行的哪一列。
type Seg struct {
	Line int // 1 起始的物理行号
	Col  int // 该段首字符在物理行中的列（1 起始）
	Off  int // 该段在逻辑行文本中的字节偏移
}

// Line 是一条逻辑行：拼接后的文本加上各段的物理位置。
type Line struct {
	Text string
	Segs []Seg
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Lines 把 s 切分成逻辑行。每个字节最多被检查两次
// （正向扫描一次，首部空白或尾部反斜杠再查一次），
// 判断续行奇偶时只从行尾回看连续反斜杠，不重扫整行。
func Lines(s string) []Line {
	var out []Line
	var text []byte
	var segs []Seg
	cont := false // 上一条物理行以奇数个反斜杠结尾
	lineNo := 0
	for i := 0; i < len(s); {
		lineNo++
		start := i
		for i < len(s) && s[i] != '\n' && s[i] != '\r' {
			checked++
			i++
		}
		raw := s[start:i]
		if i < len(s) { // 吃掉 \n、\r 或 \r\n
			checked++
			if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				checked++
				i++
			}
			i++
		}
		j := 0
		for j < len(raw) && isBlank(raw[j]) {
			checked++
			j++
		}
		body := raw[j:]
		if !cont { // 新逻辑行的起点：跳过空行与注释行
			if len(body) == 0 {
				continue
			}
			checked++
			if body[0] == '#' || body[0] == '!' {
				continue
			}
		}
		n := 0 // 从行尾回看连续反斜杠的个数
		for k := len(body) - 1; k >= 0 && body[k] == '\\'; k-- {
			checked++
			n++
		}
		cont = n%2 == 1
		if cont {
			body = body[:len(body)-1]
		}
		text = append(text, body...)
		segs = append(segs, Seg{Line: lineNo, Col: j + 1, Off: len(text) - len(body)})
		if !cont {
			if len(text) > 0 { // 与 Java 一致：不产出空逻辑行
				out = append(out, Line{Text: string(text), Segs: segs})
			}
			text, segs = nil, nil
		}
	}
	if len(text) > 0 { // 文件末尾挂着续行：反斜杠已被丢弃
		out = append(out, Line{Text: string(text), Segs: segs})
	}
	return out
}
