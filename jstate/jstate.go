// Package jstate 按连接键索引 L、R 两张表的有序 ID 集合与行内容。
package jstate

import "sort"

// Row 是一行数据：ID 在表内唯一，Key 是连接键。
type Row struct{ ID, Key string }

// State 保存两张表：按 ID 查行 + 每个连接键下按字典序升序的 ID 列表。
type State struct {
	lRows, rRows map[string]Row
	lByKey       map[string][]string
	rByKey       map[string][]string
}

func New() *State {
	return &State{
		lRows:  map[string]Row{},
		rRows:  map[string]Row{},
		lByKey: map[string][]string{},
		rByKey: map[string][]string{},
	}
}

func ins(ids []string, id string) []string {
	i := sort.SearchStrings(ids, id)
	return append(ids[:i], append([]string{id}, ids[i:]...)...)
}

func del(ids []string, id string) []string {
	i := sort.SearchStrings(ids, id)
	return append(ids[:i], ids[i+1:]...)
}

func (s *State) HasL(id string) bool { _, ok := s.lRows[id]; return ok }
func (s *State) HasR(id string) bool { _, ok := s.rRows[id]; return ok }
func (s *State) LRow(id string) Row  { return s.lRows[id] }
func (s *State) RRow(id string) Row  { return s.rRows[id] }

// N 返回 R 中连接键为 k 的行数。
func (s *State) N(k string) int { return len(s.rByKey[k]) }

// LIDs / RIDs 返回键 k 下按字典序升序的 ID 列表（只读，勿改）。
func (s *State) LIDs(k string) []string { return s.lByKey[k] }
func (s *State) RIDs(k string) []string { return s.rByKey[k] }

// Total 返回两表行数合计。
func (s *State) Total() int { return len(s.lRows) + len(s.rRows) }

func (s *State) InsertL(r Row) {
	s.lRows[r.ID] = r
	s.lByKey[r.Key] = ins(s.lByKey[r.Key], r.ID)
}
func (s *State) InsertR(r Row) {
	s.rRows[r.ID] = r
	s.rByKey[r.Key] = ins(s.rByKey[r.Key], r.ID)
}
func (s *State) DeleteL(id string) {
	r := s.lRows[id]
	delete(s.lRows, id)
	s.lByKey[r.Key] = del(s.lByKey[r.Key], id)
}
func (s *State) DeleteR(id string) {
	r := s.rRows[id]
	delete(s.rRows, id)
	s.rByKey[r.Key] = del(s.rByKey[r.Key], id)
}

// AllL 返回全部 L 行（ID 升序）。
func (s *State) AllL() []Row {
	ids := make([]string, 0, len(s.lRows))
	for id := range s.lRows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.lRows[id])
	}
	return out
}

// Clone 深拷贝，用于批处理失败时整体回滚。
func (s *State) Clone() *State {
	c := New()
	for k, v := range s.lRows {
		c.lRows[k] = v
	}
	for k, v := range s.rRows {
		c.rRows[k] = v
	}
	for k, v := range s.lByKey {
		c.lByKey[k] = append([]string(nil), v...)
	}
	for k, v := range s.rByKey {
		c.rByKey[k] = append([]string(nil), v...)
	}
	return c
}
