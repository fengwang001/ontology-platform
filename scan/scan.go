// Package scan 实现谓词下推扫描器。
//
// 扫描器先用行组统计裁剪不可能命中的行组（不解码），
// 只对可能命中的行组解码并逐行套用谓词。非导出计数器
// 记录实际解码的行组数与值个数，证明裁剪真实发生。
// 扫描可分批进行：每批返回一个游标，下次从游标继续，
// 跨批结果与一次性扫描完全一致。
package scan

import (
	"sync/atomic"

	"ontology/segment"
	"ontology/zone"
)

// Row 是命中谓词的一行。Group/Offset 定位行在段内的位置。
type Row struct {
	Group  int
	Offset int
	V      int64
	Null   bool
}

// Scanner 在单个只读段上执行谓词下推扫描。
// Scanner 可并发使用；每个扫描序列应持有自己的游标。
type Scanner struct {
	seg  *segment.Segment
	pred zone.Conjunction
	// 非导出计数器：证明裁剪真实发生（原子更新，并发安全）。
	decodedGroups atomic.Int64
	decodedValues atomic.Int64
}

// New 创建扫描器。
func New(seg *segment.Segment, pred zone.Conjunction) *Scanner {
	return &Scanner{seg: seg, pred: pred}
}

// DecodedGroups 返回至今实际解码的行组数（只读查询不会增加它）。
func (s *Scanner) DecodedGroups() int64 { return s.decodedGroups.Load() }

// DecodedValues 返回至今实际解码的值个数。
func (s *Scanner) DecodedValues() int64 { return s.decodedValues.Load() }

// Scan 从段首开始扫描，等价于 ScanFrom(起始游标, limit)。
func (s *Scanner) Scan(limit int) ([]Row, Cursor, error) {
	return s.ScanFrom(Cursor{}, limit)
}

// ScanAll 一次性扫描全部命中行。
func (s *Scanner) ScanAll() ([]Row, error) {
	rows, _, err := s.ScanFrom(Cursor{}, 0)
	return rows, err
}

// ScanFrom 从游标 cur 继续扫描，最多返回 limit 行（limit<=0 表示不限）。
// 返回的游标指向下一未消费行，可用 Done 判定是否结束。
func (s *Scanner) ScanFrom(cur Cursor, limit int) ([]Row, Cursor, error) {
	var rows []Row
	g, off := cur.group, cur.offset
	ng := s.seg.GroupCount()
	for g < ng && (limit <= 0 || len(rows) < limit) {
		st, err := s.seg.GroupStats(g)
		if err != nil {
			return nil, cur, err
		}
		if !st.MayMatch(s.pred) {
			g, off = g+1, 0 // 裁剪：整组跳过，不解码
			continue
		}
		vals, err := s.seg.DecodeGroup(g)
		if err != nil {
			return nil, cur, err
		}
		s.decodedGroups.Add(1)
		s.decodedValues.Add(int64(len(vals)))
		i := off
		for i < len(vals) && (limit <= 0 || len(rows) < limit) {
			if s.pred.Match(vals[i].V, vals[i].Null) {
				rows = append(rows, Row{Group: g, Offset: i, V: vals[i].V, Null: vals[i].Null})
			}
			i++
		}
		if i == len(vals) {
			g, off = g+1, 0
		} else {
			off = i
		}
	}
	return rows, Cursor{group: g, offset: off}, nil
}

// Done 报告游标是否已越过最后一个行组（扫描结束）。
func (s *Scanner) Done(cur Cursor) bool {
	return cur.group >= s.seg.GroupCount()
}
