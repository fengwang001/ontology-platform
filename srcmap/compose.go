package srcmap

// compose 合成 m2（最终→中间）与 m1（中间→原始）。
//
// 算法：按 M2 的行、段顺序各扫描一次。对每个 M2 段，先用二分定位其
// 中间入口列在 M1 中命中的段，再只在“被真正跨越的 M1 段起点”处切分。
// M1 段指针只进不退，因此总工作量为 O(|M2| + 被跨越的 M1 边界数 + 输出段数)，
// 不依赖两张映射段数的乘积，也不依赖行列号数值。
func compose(m2, m1 *Mapping) (*Mapping, error) {
	if err := requireFinalToIntermediate(m2); err != nil {
		return nil, err
	}

	result := make([]Line, 0, len(m2.lines))
	for li2 := range m2.lines {
		line2 := &m2.lines[li2]
		segs2 := line2.Segments
		raw := make([]Segment, 0, len(segs2))
		for si2 := range segs2 {
			seg2 := &segs2[si2]
			// M2 本段在最终列上覆盖 [start, limit)。
			limit := MaxCoord + 1
			if si2+1 < len(segs2) {
				limit = segs2[si2+1].Start
			}
			if seg2.Unmapped {
				raw = append(raw, Segment{Start: seg2.Start, Unmapped: true})
				continue
			}
			m1LineIdx, hasLine := m1.findLine(seg2.OrigLine)
			var segs1 []Segment
			if hasLine {
				segs1 = m1.lines[m1LineIdx].Segments
			}
			c := seg2.Start
			// M1 中起点 <= 中间入口列 的最后一段。
			j := -1
			if hasLine {
				j = sortSearch(len(segs1), func(k int) bool { return segs1[k].Start > seg2.OrigCol }) - 1
			}
			for c < limit {
				midCol := seg2.OrigCol + (c - seg2.Start)
				nextChange := -1
				if k := j + 1; k < len(segs1) {
					nextChange = segs1[k].Start
				}
				end := limit
				if nextChange >= 0 {
					if bc := c + (nextChange - midCol); bc < end {
						end = bc
					}
				}
				cur := Segment{Start: c}
				mapped := false
				if j >= 0 && !segs1[j].Unmapped {
					cur.SourceIndex = segs1[j].SourceIndex
					cur.OrigLine = segs1[j].OrigLine
					cur.OrigCol = segs1[j].OrigCol + (midCol - segs1[j].Start)
					mapped = true
					// 溢出只可能首次出现在某个输出段的起点：段内原始列线性增长，
					// 下一个被检查的起点即下一个变化点。末段向行尾的延伸属于
					// 无限模型本身（查询越界由 Lookup 的位置检查负责），不在此强制收口。
					if cur.OrigCol > MaxCoord {
						return nil, overflowf("合成结果在最终位置 (%d,%d) 的原始列 %d 超出 [0,%d]",
							line2.GeneratedLine, c, cur.OrigCol, MaxCoord)
					}
				}
				if mapped {
					// 段内原始列斜率为 +1；检查该输出段在可表示域内
					// （最终列 <= 1e9）的最远点是否溢出。
					farthest := end - 1
					if farthest > MaxCoord {
						farthest = MaxCoord
					}
					if farthest >= c && cur.OrigCol+(farthest-c) > MaxCoord {
						return nil, overflowf("合成结果在最终位置 (%d,%d) 的原始列超出 %d",
							line2.GeneratedLine, c+(MaxCoord-cur.OrigCol)+1, MaxCoord)
					}
					raw = append(raw, cur)
				} else {
					raw = append(raw, Segment{Start: c, Unmapped: true})
				}
				c = end
				if nextChange >= 0 && c < limit {
					midAtEnd := seg2.OrigCol + (c - seg2.Start)
					if midAtEnd >= nextChange {
						j++
						for j+1 < len(segs1) && segs1[j+1].Start <= midAtEnd {
							j++
						}
					}
				}
			}
		}
		if canon := canonicalizeLine(raw); len(canon) > 0 {
			result = append(result, Line{GeneratedLine: line2.GeneratedLine, Segments: canon})
		}
	}
	return buildMapping(m1.sourceCount, result), nil
}

func requireFinalToIntermediate(m *Mapping) error {
	if m.sourceCount != 1 {
		return invalidf("最终→中间映射的源数量必须为 1，实际为 %d", m.sourceCount)
	}
	for _, line := range m.lines {
		for _, seg := range line.Segments {
			if !seg.Unmapped && seg.SourceIndex != 0 {
				return invalidf("最终→中间映射段的源索引必须为 0，实际为 %d", seg.SourceIndex)
			}
		}
	}
	return nil
}

// canonicalizeLine 规范化单行内已按起点递增的段序列。
func canonicalizeLine(segs []Segment) []Segment {
	out := canonicalize([]Line{{GeneratedLine: 0, Segments: segs}})
	if len(out) == 0 {
		return nil
	}
	return out[0].Segments
}
