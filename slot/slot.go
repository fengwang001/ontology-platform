// Package slot 实现复制槽：解码器、Confirm、需保留集合与 restart 计算、回收、Restart。依赖 wal。
package slot

import (
	"errors"

	"ontology/wal"
)

var ErrBackward = errors.New("slot: 确认回退")
var ErrNotBoundary = errors.New("slot: 确认非事务边界")
var ErrTooManyOpen = errors.New("slot: 进行中事务数超限")

// Txn 是发出给消费端的一个已提交事务。
type Txn struct {
	Xid       string
	CommitLSN int64
	Changes   []string
}

// ent 是需保留队列条目，按 Begin LSN 升序入队；commit<0 表示进行中。
type ent struct {
	xid           string
	begin, commit int64
	aborted       bool
	changes       []string
}

func (e *ent) retained(c int64) bool { return !e.aborted && (e.commit < 0 || e.commit > c) }

type Slot struct {
	w                  *wal.WAL
	maxOpen, head      int
	confirmed, restart int64
	open               map[string]*ent
	queue              []*ent // 按 Begin LSN 升序；head 之前是已弹出部分
	emitted            []Txn
	emittedLSN         map[int64]bool
	checked            int // 最近一次被接受的 Confirm 重算 restart 时检查过的条目数
}

// New 创建槽，两个 LSN 初始均为 5。
func New(maxOpen int) *Slot {
	return &Slot{w: wal.New(), maxOpen: maxOpen, confirmed: 5, restart: 5, open: map[string]*ent{}, emittedLSN: map[int64]bool{}}
}

func (s *Slot) Confirmed() int64  { return s.confirmed }
func (s *Slot) RestartLSN() int64 { return s.restart }
func (s *Slot) Emitted() []Txn    { return append([]Txn(nil), s.emitted...) }

// Append 先整体校验 WAL 合法性，再干跑 maxOpen，最后写入并解码；任一失败全部不生效。
func (s *Slot) Append(recs ...wal.Record) error {
	if err := s.w.Validate(recs...); err != nil {
		return err
	}
	n := len(s.open)
	for _, r := range recs {
		switch r.Kind {
		case wal.Begin:
			n++
			if n > s.maxOpen {
				return ErrTooManyOpen
			}
		case wal.Commit, wal.Abort:
			n--
		}
	}
	if err := s.w.Append(recs...); err != nil {
		return err
	}
	for _, r := range recs {
		s.decode(r, false)
	}
	return nil
}

// decode 解码一条记录；replay 时只发出提交 LSN > confirmed 的事务，Begin 已回收的事务整体忽略。
func (s *Slot) decode(r wal.Record, replay bool) {
	switch r.Kind {
	case wal.Begin:
		e := &ent{xid: r.Xid, begin: r.LSN, commit: -1}
		s.open[r.Xid] = e
		s.queue = append(s.queue, e)
	case wal.Change:
		if e := s.open[r.Xid]; e != nil {
			e.changes = append(e.changes, r.Data)
		}
	case wal.Commit:
		if e := s.open[r.Xid]; e != nil {
			delete(s.open, r.Xid)
			e.commit = r.LSN
			if !replay || r.LSN > s.confirmed {
				s.emitted = append(s.emitted, Txn{r.Xid, r.LSN, append([]string(nil), e.changes...)})
				s.emittedLSN[r.LSN] = true
			}
		} // 否则 Begin 已回收且提交 <= confirmed：忽略
	case wal.Abort:
		if e := s.open[r.Xid]; e != nil {
			delete(s.open, r.Xid)
			e.aborted = true
		}
	}
}

// Confirm 幂等成功 / 回退拒绝 / 非事务边界拒绝；接受后推进 confirmed、重算 restart 并回收 WAL。
func (s *Slot) Confirm(lsn int64) error {
	if lsn == s.confirmed {
		return nil
	}
	if lsn < s.confirmed {
		return ErrBackward
	}
	if !s.emittedLSN[lsn] {
		return ErrNotBoundary
	}
	s.confirmed = lsn
	s.recompute()
	s.w.Reclaim(s.restart)
	return nil
}

// recompute 从按 Begin 升序的队首弹出不再需保留的条目，队首即最小 Begin；检查次数与队列总长无关。
func (s *Slot) recompute() {
	s.checked = 0
	for s.head < len(s.queue) && !s.queue[s.head].retained(s.confirmed) {
		s.checked++
		s.head++
	}
	s.restart = s.confirmed
	if s.head < len(s.queue) {
		s.checked++
		if s.queue[s.head].begin < s.restart {
			s.restart = s.queue[s.head].begin
		}
	}
	if s.head > 0 {
		s.queue = append([]*ent(nil), s.queue[s.head:]...)
		s.head = 0
	}
}

// Restart 模拟崩溃重启：保留两个 LSN，丢弃解码状态，从未回收记录重读整个 WAL。
func (s *Slot) Restart() {
	s.open = map[string]*ent{}
	s.queue, s.head = nil, 0
	s.emitted, s.emittedLSN = nil, map[int64]bool{}
	for _, r := range s.w.Records() {
		s.decode(r, true)
	}
}
