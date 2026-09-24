// Package logical 把 properties 文件的物理行拼成逻辑行：
// 跳过注释行与空行，处理续行（行尾奇数个反斜杠）并剥掉续行首部空白。
package logical

// Segment 是逻辑行中来自同一物理行的一段。
type Segment struct {
	Text string // 该段的字节内容（不含行终止符）
	Line int    // 1 起始的物理行号
	Col  int    // Text 首字节在该物理行中的列号（1 起始）
}

// Line 是一条逻辑行，由一到多个 Segment 拼成。
type Line struct {
	Segs []Segment
}

// Flatten 拼接各段，返回完整文本与每段在文本中的起始偏移。
func (l Line) Flatten() (string, []int) {
	n := 0
	for _, s := range l.Segs {
		n += len(s.Text)
	}
	b := make([]byte, 0, n)
	starts := make([]int, len(l.Segs))
	for i, s := range l.Segs {
		starts[i] = len(b)
		b = append(b, s.Text...)
	}
	return string(b), starts
}

// String 把各段拼成完整逻辑行文本。
func (l Line) String() string {
	s, _ := l.Flatten()
	return s
}

// Pos 返回逻辑行内偏移 off 对应的物理行号与列号（starts 来自 Flatten）。
func (l Line) Pos(starts []int, off int) (int, int) {
	i := len(starts) - 1
	for i > 0 && starts[i] > off {
		i--
	}
	sg := l.Segs[i]
	return sg.Line, sg.Col + off - starts[i]
}

// Scanner 切分逻辑行，并用非导出计数器记录字节被检查的总次数。
type Scanner struct {
	checked int
}

// Checked 返回字节被检查的总次数。
func (s *Scanner) Checked() int { return s.checked }

// Split 把 data 切成逻辑行（已剔除注释行与空行）。
// 行终止符为 \n、\r 或 \r\n。每个字节最多被检查两次：
// 第一遍找物理行边界一次，第二遍只复查前导空白与行尾反斜杠。
func (s *Scanner) Split(data []byte) []Line {
	type phys struct{ start, end int } // end 不含行终止符
	var lines []phys
	start := 0
	skipLF := false
	for i := 0; i < len(data); i++ {
		s.checked++
		switch data[i] {
		case '\r':
			lines = append(lines, phys{start, i})
			start = i + 1
			skipLF = true
		case '\n':
			if skipLF {
				skipLF = false
				start = i + 1
				continue
			}
			lines = append(lines, phys{start, i})
			start = i + 1
		default:
			skipLF = false
		}
	}
	if start < len(data) {
		lines = append(lines, phys{start, len(data)})
	}
	var out []Line
	var cur []Segment
	continuing := false
	for n, pl := range lines {
		p := pl.start
		var b byte
		for p < pl.end { // 前导空白（' ' '\t' '\f'）
			s.checked++
			b = data[p]
			if b != ' ' && b != '\t' && b != '\f' {
				break
			}
			p++
		}
		if !continuing {
			if p == pl.end {
				continue // 空行
			}
			if b == '#' || b == '!' {
				continue // 注释行
			}
		}
		q := pl.end
		for q > p { // 只从行尾回扫连续反斜杠，判断奇偶
			s.checked++
			if data[q-1] != '\\' {
				break
			}
			q--
		}
		segEnd := pl.end
		if odd := (pl.end-q)%2 == 1; odd {
			segEnd-- // 奇数个：丢掉续行反斜杠，续到下一行
			if segEnd > p {
				cur = append(cur, Segment{Text: string(data[p:segEnd]), Line: n + 1, Col: p - pl.start + 1})
			}
			continuing = true
			continue
		}
		if segEnd > p {
			cur = append(cur, Segment{Text: string(data[p:segEnd]), Line: n + 1, Col: p - pl.start + 1})
		}
		out = append(out, Line{Segs: cur})
		cur = nil
		continuing = false
	}
	if len(cur) > 0 { // EOF：续行反斜杠已丢弃，直接收尾
		out = append(out, Line{Segs: cur})
	}
	return out
}
