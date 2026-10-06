package srcmap

// MaxCoord 是行、列、原始行、原始列允许的最大值（闭区间上界）。
const MaxCoord = 1_000_000_000

// Segment 表示生成行中的一段。
// Unmapped 为 true 时为显式未映射段，SourceIndex/OrigLine/OrigCol 无意义。
type Segment struct {
	Start       int  `json:"start"`
	Unmapped    bool `json:"unmapped,omitempty"`
	SourceIndex int  `json:"sourceIndex,omitempty"`
	OrigLine    int  `json:"origLine,omitempty"`
	OrigCol     int  `json:"origCol,omitempty"`
}

// Position 是一个原始位置。
type Position struct {
	SourceIndex int
	Line        int
	Column      int
}

// LookupResult 是点查询的结果。Mapped 为 false 表示未映射。
type LookupResult struct {
	Mapped   bool
	Position Position
}

// Line 是一行：生成行号加严格按起点递增的段序列。
type Line struct {
	GeneratedLine int
	Segments      []Segment
}

// Mapping 是一张源码映射。零值不可直接使用，请用 New 构造。
type Mapping struct {
	sourceCount int
	lines       []Line
	// starts 与 lines 一一对应，缓存每行段起点，供二分查找。
	starts [][]int
}

// SourceCount 返回源数量。
func (m *Mapping) SourceCount() int { return m.sourceCount }

// Lines 返回规范后的行（只读使用，不得修改）。
func (m *Mapping) Lines() []Line { return m.lines }

// New 校验输入并返回规范化后的不可变映射。
func New(sourceCount int, lines []Line) (*Mapping, error) {
	if sourceCount < 0 {
		return nil, invalidf("sourceCount 必须为非负整数，实际为 %d", sourceCount)
	}
	copied := make([]Line, len(lines))
	for i := range lines {
		copied[i] = Line{GeneratedLine: lines[i].GeneratedLine, Segments: append([]Segment(nil), lines[i].Segments...)}
	}
	if err := validate(sourceCount, copied); err != nil {
		return nil, err
	}
	canon := canonicalize(copied)
	return buildMapping(sourceCount, canon), nil
}

// Lookup 对生成位置做点查询。
func (m *Mapping) Lookup(genLine, genCol int) (LookupResult, error) {
	if err := checkGenPos(genLine, genCol); err != nil {
		return LookupResult{}, err
	}
	idx, ok := m.findLine(genLine)
	if !ok {
		return LookupResult{Mapped: false}, nil
	}
	segs := m.lines[idx].Segments
	// 取起点 <= genCol 的最后一段。
	j := sortSearch(len(segs), func(k int) bool { return m.starts[idx][k] > genCol }) - 1
	if j < 0 {
		return LookupResult{Mapped: false}, nil
	}
	seg := segs[j]
	if seg.Unmapped {
		return LookupResult{Mapped: false}, nil
	}
	origCol := seg.OrigCol + (genCol - seg.Start)
	if origCol > MaxCoord {
		return LookupResult{}, overflowf("原始列 %d 超过 %d", origCol, MaxCoord)
	}
	return LookupResult{Mapped: true, Position: Position{
		SourceIndex: seg.SourceIndex,
		Line:        seg.OrigLine,
		Column:      origCol,
	}}, nil
}

// LookResult 是 Lookup 的结果类型别名。
type LookResult = LookupResult

// Compose 合成 m2（最终→中间）与 m1（中间→原始）。
func Compose(m2, m1 *Mapping) (*Mapping, error) {
	return compose(m2, m1)
}
