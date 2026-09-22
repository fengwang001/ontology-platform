package segment

import "ontology/zone"

// Segment 是段的只读视图，可安全地被多 goroutine 并发使用。
type Segment struct {
	data   []byte
	rows   int
	groups []groupLoc
}

// Open 解析段字节，返回只读视图。段级头部损坏时返回
// CorruptError{Group: -1}；行组级损坏在访问该行组时才报告。
func Open(data []byte) (*Segment, error) {
	p := &parser{data: data, limit: len(data), group: -1, stage: StageHeader}
	for i, want := range []byte{magic0, magic1, magic2, magic3, version} {
		b, err := p.byte()
		if err != nil {
			return nil, err
		}
		if b != want {
			return nil, p.fail(magicErr(i))
		}
	}
	nGroups, err := p.uvarint()
	if err != nil {
		return nil, err
	}
	totalRows, err := p.uvarint()
	if err != nil {
		return nil, err
	}
	if nGroups > uint64(len(data)) || totalRows > uint64(maxInt()) {
		return nil, p.fail("implausible header counts")
	}
	lens := make([]int, int(nGroups))
	for i := range lens {
		blkLen, err := p.uvarint()
		if err != nil {
			return nil, err
		}
		if blkLen > uint64(len(data)) {
			return nil, p.fail("implausible block length")
		}
		lens[i] = int(blkLen)
	}
	groups := make([]groupLoc, int(nGroups))
	pos := p.pos
	for i := range groups {
		groups[i] = groupLoc{start: pos, length: lens[i]}
		pos += lens[i]
	}
	return &Segment{data: data, rows: int(totalRows), groups: groups}, nil
}

func magicErr(i int) string {
	if i < 4 {
		return "bad magic"
	}
	return "unsupported version"
}

func maxInt() int { return int(^uint(0) >> 1) }

// RowCount 返回段的总行数。
func (s *Segment) RowCount() int { return s.rows }

// GroupCount 返回行组数。
func (s *Segment) GroupCount() int { return len(s.groups) }

// Bytes 返回段字节的副本。
func (s *Segment) Bytes() []byte {
	out := make([]byte, len(s.data))
	copy(out, s.data)
	return out
}

// Truncated 返回段字节截断到前 n 字节的副本，用于损坏测试。
func (s *Segment) Truncated(n int) []byte {
	if n < 0 {
		n = 0
	}
	if n > len(s.data) {
		n = len(s.data)
	}
	out := make([]byte, n)
	copy(out, s.data[:n])
	return out
}

// groupParser 为行组 g 构造解析器。块声明长度超出实际数据时
// 以实际数据为界，解析到越界处报当前阶段的损坏错误。
func (s *Segment) groupParser(g int) (*parser, error) {
	if g < 0 || g >= len(s.groups) {
		return nil, ErrGroupOutOfRange
	}
	loc := s.groups[g]
	limit := loc.start + loc.length
	if limit > len(s.data) {
		limit = len(s.data)
	}
	return &parser{data: s.data, pos: loc.start, limit: limit, group: g}, nil
}

// GroupStats 返回行组 g 的统计。只解析头部与统计区，不解码数据。
func (s *Segment) GroupStats(g int) (zone.Stats, error) {
	p, err := s.groupParser(g)
	if err != nil {
		return zone.Stats{}, err
	}
	h, err := s.parseGroupHeader(p)
	if err != nil {
		return zone.Stats{}, err
	}
	st, err := parseGroupStats(p)
	if err != nil {
		return zone.Stats{}, err
	}
	st.Rows, st.Nulls = h.rows, h.nulls
	return st, nil
}

// GroupEncoding 返回行组 g 的编码类型。只解析头部，不解码数据。
func (s *Segment) GroupEncoding(g int) (Encoding, error) {
	p, err := s.groupParser(g)
	if err != nil {
		return 0, err
	}
	h, err := s.parseGroupHeader(p)
	if err != nil {
		return 0, err
	}
	return h.enc, nil
}
