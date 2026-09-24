package scan

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"ontology/segment"
	"ontology/zone"
)

var ErrCursor = errors.New("scan: invalid cursor")

type Result struct {
	Index int
	Value int64
	Valid bool
}

type Cursor struct {
	Group  int
	Offset int
}

type Stats struct {
	DecodedGroups int
	DecodedValues int
}

type Scanner struct {
	seg     *segment.Segment
	pred    *zone.Predicate
	cursor  Cursor
	decoded map[int][]segment.Int64Value
	stats   Stats
}

func New(seg *segment.Segment, pred *zone.Predicate) *Scanner {
	return &Scanner{seg: seg, pred: pred, decoded: make(map[int][]segment.Int64Value)}
}

func NewAt(seg *segment.Segment, pred *zone.Predicate, encoded string) (*Scanner, error) {
	cursor := Cursor{}
	if encoded != "" {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return nil, ErrCursor
		}
		parts := strings.Split(string(raw), ":")
		if len(parts) != 2 {
			return nil, ErrCursor
		}
		group, err := strconv.Atoi(parts[0])
		offset, err2 := strconv.Atoi(parts[1])
		if err != nil || err2 != nil {
			return nil, ErrCursor
		}
		cursor = Cursor{Group: group, Offset: offset}
	}
	if cursor.Group < 0 || cursor.Offset < 0 {
		return nil, ErrCursor
	}
	return &Scanner{seg: seg, pred: pred, cursor: cursor, decoded: make(map[int][]segment.Int64Value)}, nil
}

func (s *Scanner) Cursor() string {
	raw := strconv.Itoa(s.cursor.Group) + ":" + strconv.Itoa(s.cursor.Offset)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (s *Scanner) Stats() Stats { return s.stats }

func (s *Scanner) Next(limit int) ([]Result, string, error) {
	if limit <= 0 {
		return nil, s.Cursor(), nil
	}
	info := s.seg.Info()
	if s.cursor.Group > info.Groups || (s.cursor.Group == info.Groups && s.cursor.Offset != 0) {
		return nil, "", ErrCursor
	}
	results := make([]Result, 0, min(limit, 16))
	for group := s.cursor.Group; group < info.Groups && len(results) < limit; group++ {
		exhausted := false
		stats, _ := s.seg.Int64Stats(group)
		start := 0
		if group == s.cursor.Group {
			start = s.cursor.Offset
			if start > stats.Rows {
				return nil, "", ErrCursor
			}
		}
		if !s.pred.KeepInt64(stats) {
			s.cursor = Cursor{Group: group + 1, Offset: 0}
			continue
		}
		values, ok := s.decoded[group]
		if !ok {
			decoded, err := s.seg.DecodeInt64Group(group)
			if err != nil {
				return nil, "", err
			}
			values = decoded
			s.decoded[group] = values
			s.stats.DecodedGroups++
			s.stats.DecodedValues += len(values)
		}
		base := 0
		for i := 0; i < group; i++ {
			if prior, ok := s.seg.Int64Stats(i); ok {
				base += prior.Rows
			}
		}
		for offset := start; offset < len(values) && len(results) < limit; offset++ {
			row := values[offset]
			matched := false
			if row.Valid && s.pred.Op != zone.OpNull {
				matched = s.pred.MatchInt64(row.Value)
			}
			if isNullTest(s.pred, zone.OpNull) && !row.Valid {
				matched = true
			}
			if isNullTest(s.pred, zone.OpNotNull) && row.Valid {
				matched = true
			}
			if matched {
				results = append(results, Result{Index: base + offset, Value: row.Value, Valid: row.Valid})
			}
			s.cursor = Cursor{Group: group, Offset: offset + 1}
		}
		exhausted = s.cursor.Group == group && s.cursor.Offset >= len(values)
		if exhausted {
			s.cursor = Cursor{Group: group + 1, Offset: 0}
		}
		start = 0
	}
	return results, s.Cursor(), nil
}

func isNullTest(pred *zone.Predicate, op int) bool {
	if pred.Op == op {
		return true
	}
	if pred.Op == zone.OpAnd {
		for _, kid := range pred.Kids {
			if isNullTest(kid, op) {
				return true
			}
		}
	}
	return false
}
