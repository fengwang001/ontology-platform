// Package api 对外暴露三副本集群接口。依赖 raft。
package api

import (
	"sync"

	"ontology/entry"
	"ontology/raft"
)

// 三类可判定哨兵错误，互不相同（定义在 raft，此处再导出）。
var (
	ErrEmptyCmd         = raft.ErrEmptyCmd
	ErrPrevIndexRange   = raft.ErrPrevIndexRange
	ErrPrevTermMismatch = raft.ErrPrevTermMismatch
)

type cluster struct {
	mu sync.RWMutex
	r  [3]raft.Replica
}

// Server 是集群中一个副本的句柄。
type Server struct {
	c  *cluster
	id int
}

// New 创建三副本集群，返回值依次对应 S1、S2、S3。
func New() (sv [3]*Server) {
	c := new(cluster)
	for i := range sv {
		sv[i] = &Server{c, i}
	}
	return
}

// SetTerm 设置副本当前任期（由场景指定）。
func (s *Server) SetTerm(t int) { s.c.mu.Lock(); s.c.r[s.id].Term = t; s.c.mu.Unlock() }

// Term 返回副本当前任期。
func (s *Server) Term() int { s.c.mu.RLock(); defer s.c.mu.RUnlock(); return s.c.r[s.id].Term }

// Append 让 server（作为领导者）以当前任期追加一条命令。
func Append(s *Server, cmd string) error {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	_, err := s.c.r[s.id].Append(cmd)
	return err
}

// Replicate 把 leader 的 log[prevIndex+1..] 复制给 follower。
func Replicate(leader, follower *Server, prevIndex int) error {
	leader.c.mu.Lock()
	defer leader.c.mu.Unlock()
	return raft.Replicate(&leader.c.r[leader.id], &leader.c.r[follower.id], prevIndex)
}

// CommitIndex 对 leader 做多数派 + 当前任期的提交判定，返回新的 commitIndex。
func CommitIndex(leader *Server) int {
	leader.c.mu.Lock()
	defer leader.c.mu.Unlock()
	c := leader.c
	return raft.CommitIndex(&c.r[leader.id], []*raft.Replica{&c.r[0], &c.r[1], &c.r[2]})
}

// Committed 返回 server 已提交的 commitIndex（不重新判定）。
func Committed(s *Server) int {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	return s.c.r[s.id].Committed()
}

// Log 返回 server 日志快照，可并发调用。
func Log(s *Server) []entry.Entry {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	return s.c.r[s.id].Log()
}
