package srcmap

import "sort"

func sortSearch(n int, f func(int) bool) int { return sort.Search(n, f) }

func inRange(v int) bool { return v >= 0 && v <= MaxCoord }

func checkGenPos(line, col int) error {
	if !inRange(line) || !inRange(col) {
		return invalidf("查询位置 (%d,%d) 越界，允许范围为 [0,%d]", line, col, MaxCoord)
	}
	return nil
}

// validate 检查全部输入约束（行与段），不修改内容。
func validate(sourceCount int, lines []Line) error {
	lastLine := -1
	for li := range lines {
		line := &lines[li]
		if !inRange(line.GeneratedLine) {
			return invalidf("生成行号 %d 越界", line.GeneratedLine)
		}
		if line.GeneratedLine <= lastLine {
			return invalidf("生成行号必须严格递增，第 %d 行处出错（值 %d）", li, line.GeneratedLine)
		}
		lastLine = line.GeneratedLine
		lastStart := -1
		for si, seg := range line.Segments {
			if !inRange(seg.Start) {
				return invalidf("行 %d 第 %d 段起点 %d 越界", line.GeneratedLine, si, seg.Start)
			}
			if seg.Start <= lastStart {
				return invalidf("行 %d 的生成列必须严格递增，段 %d 起点 %d 出错", line.GeneratedLine, si, seg.Start)
			}
			lastStart = seg.Start
			if seg.Unmapped {
				continue
			}
			if seg.SourceIndex < 0 || seg.SourceIndex >= sourceCount {
				return invalidf("行 %d 段 %d 的源索引 %d 超出源数量 %d", line.GeneratedLine, si, seg.SourceIndex, sourceCount)
			}
			if !inRange(seg.OrigLine) {
				return invalidf("行 %d 段 %d 的原始行 %d 越界", line.GeneratedLine, si, seg.OrigLine)
			}
			if !inRange(seg.OrigCol) {
				return invalidf("行 %d 段 %d 的原始列 %d 越界", line.GeneratedLine, si, seg.OrigCol)
			}
		}
	}
	return nil
}

// buildMapping 由已规范化的行构造映射并建立查找索引。
func buildMapping(sourceCount int, lines []Line) *Mapping {
	starts := make([][]int, len(lines))
	for i := range lines {
		segs := lines[i].Segments
		arr := make([]int, len(segs))
		for j := range segs {
			arr[j] = segs[j].Start
		}
		starts[i] = arr
	}
	return &Mapping{sourceCount: sourceCount, lines: lines, starts: starts}
}

func (m *Mapping) findLine(genLine int) (int, bool) {
	idx := sort.Search(len(m.lines), func(i int) bool { return m.lines[i].GeneratedLine >= genLine })
	if idx < len(m.lines) && m.lines[idx].GeneratedLine == genLine {
		return idx, true
	}
	return 0, false
}

// canonicalize 输出唯一最简形式：
//   - 行首未映射段省略；
//   - 相邻未映射段合并（仅保留第一个）；
//   - 相邻可“向后延伸”的映射段合并；
//   - 空行省略。
//
// 输入行本身按行号递增、段起点递增。
func canonicalize(lines []Line) []Line {
	out := make([]Line, 0, len(lines))
	for _, line := range lines {
		kept := make([]Segment, 0, len(line.Segments))
		for _, seg := range line.Segments {
			if len(kept) == 0 {
				if seg.Unmapped {
					continue // 行首未映射省略
				}
				kept = append(kept, seg)
				continue
			}
			prev := kept[len(kept)-1]
			switch {
			case prev.Unmapped && seg.Unmapped:
				// 连续未映射，保留第一个（丢弃本段起点）。
				continue
			case !prev.Unmapped && !seg.Unmapped &&
				prev.SourceIndex == seg.SourceIndex && prev.OrigLine == seg.OrigLine &&
				prev.OrigCol+(seg.Start-prev.Start) == seg.OrigCol:
				// 后一段恰为前一段的延伸，丢弃本段起点。
				continue
			default:
				kept = append(kept, seg)
			}
		}
		if len(kept) > 0 {
			out = append(out, Line{GeneratedLine: line.GeneratedLine, Segments: kept})
		}
	}
	return out
}
