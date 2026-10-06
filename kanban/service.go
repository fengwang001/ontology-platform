package kanban

import (
	"fmt"
	"io"
	"sync"
)

// LogEntry 记录一次操作的输入、输出与判定依据。
type LogEntry struct {
	Op       string
	Input    string
	Accepted bool
	Output   string // 成功时为结果摘要，拒绝时为错误原因
	Code     ErrCode
}

func (e LogEntry) String() string {
	status := "ACCEPTED"
	if !e.Accepted {
		status = "REJECTED"
	}
	return fmt.Sprintf("%s\t%s\tinput={%s}\t%s", status, e.Op, e.Input, e.Output)
}

// Service 以单把互斥锁串行化所有变更操作，保证并发调用
// 等价于某个确定的串行顺序；所有判定均在锁内对同一状态完成。
type Service struct {
	mu      sync.Mutex
	board   *Board
	logSink io.Writer
	logMu   sync.Mutex
	logs    []LogEntry
}

// NewService 创建看板服务。logSink 非 nil 时每条日志同步写入（测试/复现用）。
func NewService(cfg Config, logSink io.Writer) (*Service, error) {
	b, err := NewBoard(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{board: b, logSink: logSink}, nil
}

// Board 返回内部看板（仅供只读检查；调用方不得在锁外变更）。
func (s *Service) Board() *Board { return s.board }

// GetCard 在服务锁内读取卡片快照，供并发场景安全轮询版本。
func (s *Service) GetCard(id string) (*Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.board.GetCard(id)
}

func (s *Service) emit(e LogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logs = append(s.logs, e)
	sink := s.logSink
	if sink != nil {
		fmt.Fprintln(sink, e.String())
	}
}

// Logs 返回截至当前全部日志的拷贝。
func (s *Service) Logs() []LogEntry {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]LogEntry, len(s.logs))
	copy(out, s.logs)
	return out
}

// AddCard 见 Board.AddCard。
func (s *Service) AddCard(id, owner string, now int64) (c *Card, err error) {
	input := fmt.Sprintf("id=%q owner=%q now=%d", id, owner, now)
	s.mu.Lock()
	c, err = s.board.AddCard(id, owner, now)
	s.mu.Unlock()
	s.record("AddCard", input, c, err)
	return c, err
}

// Move 见 Board.Move。
func (s *Service) Move(user, cardID string, to, expectVer int, expedite bool, now int64) (c *Card, err error) {
	input := fmt.Sprintf("user=%q card=%q to=%d expectVer=%d expedite=%t now=%d", user, cardID, to, expectVer, expedite, now)
	s.mu.Lock()
	c, err = s.board.Move(user, cardID, to, expectVer, expedite, now)
	s.mu.Unlock()
	s.record("Move", input, c, err)
	return c, err
}

// Reopen 见 Board.Reopen。
func (s *Service) Reopen(user, cardID string, expectVer int, now int64) (c *Card, err error) {
	input := fmt.Sprintf("user=%q card=%q expectVer=%d now=%d", user, cardID, expectVer, now)
	s.mu.Lock()
	c, err = s.board.Reopen(user, cardID, expectVer, now)
	s.mu.Unlock()
	s.record("Reopen", input, c, err)
	return c, err
}

// ChangeOwner 见 Board.ChangeOwner。
func (s *Service) ChangeOwner(user, cardID, newOwner string, expectVer int, now int64) (c *Card, err error) {
	input := fmt.Sprintf("user=%q card=%q newOwner=%q expectVer=%d now=%d", user, cardID, newOwner, expectVer, now)
	s.mu.Lock()
	c, err = s.board.ChangeOwner(user, cardID, newOwner, expectVer, now)
	s.mu.Unlock()
	s.record("ChangeOwner", input, c, err)
	return c, err
}

// AddDep 见 Board.AddDep。
func (s *Service) AddDep(user, cardID, prerequisiteID string, expectVer int, now int64) (err error) {
	input := fmt.Sprintf("user=%q card=%q prereq=%q expectVer=%d now=%d", user, cardID, prerequisiteID, expectVer, now)
	s.mu.Lock()
	err = s.board.AddDep(user, cardID, prerequisiteID, expectVer, now)
	s.mu.Unlock()
	s.recordNoCard("AddDep", input, err)
	return err
}

// RemoveDep 见 Board.RemoveDep。
func (s *Service) RemoveDep(user, cardID, prerequisiteID string, expectVer int, now int64) (err error) {
	input := fmt.Sprintf("user=%q card=%q prereq=%q expectVer=%d now=%d", user, cardID, prerequisiteID, expectVer, now)
	s.mu.Lock()
	err = s.board.RemoveDep(user, cardID, prerequisiteID, expectVer, now)
	s.mu.Unlock()
	s.recordNoCard("RemoveDep", input, err)
	return err
}

// SetColumnLimit 见 Board.SetColumnLimit。
func (s *Service) SetColumnLimit(col, limit int, now int64) (err error) {
	input := fmt.Sprintf("col=%d limit=%d now=%d", col, limit, now)
	s.mu.Lock()
	err = s.board.SetColumnLimit(col, limit, now)
	s.mu.Unlock()
	s.recordNoCard("SetColumnLimit", input, err)
	return err
}

func (s *Service) record(op, input string, c *Card, err error) {
	if err != nil {
		s.emit(LogEntry{Op: op, Input: input, Accepted: false, Output: err.Error(), Code: codeOf(err)})
		return
	}
	out := fmt.Sprintf("card=%q owner=%q col=%d version=%d expedite=%t prereqs=%v", c.ID, c.Owner, c.Column, c.Version, c.Expedited, c.Prereqs)
	s.emit(LogEntry{Op: op, Input: input, Accepted: true, Output: out})
}

func (s *Service) recordNoCard(op, input string, err error) {
	if err != nil {
		s.emit(LogEntry{Op: op, Input: input, Accepted: false, Output: err.Error(), Code: codeOf(err)})
		return
	}
	s.emit(LogEntry{Op: op, Input: input, Accepted: true, Output: "ok"})
}

func codeOf(err error) ErrCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ErrInvalidArgument
}
