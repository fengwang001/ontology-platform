// Package upload 实现分片上传会话的登记与完成校验。
//
// 会话创建时声明总片数 N 与单片上限 maxChunkSize，分片编号取 1..N。
// 非末片大小必须等于上限，末片大小须在 1..上限 之间。同号再传视为覆盖：
// 字节数减旧加新，覆盖次数加一。完成时若有缺片，按编号升序一次列全并拒绝，
// 已登记分片保留可补传；完成后会话冻结，查询仍如实返回统计。
package upload

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。多因同时成立时按下列固定优先级只报第一个：
// 不存在 > 已完成 > 越界 > 大小。
var (
	// ErrInvalidArgument 创建参数非正（总片数或单片上限 <= 0）。
	ErrInvalidArgument = errors.New("upload: 创建参数必须为正")
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = errors.New("upload: 会话不存在")
	// ErrSessionCompleted 会话已完成（含重复完成），已冻结。
	ErrSessionCompleted = errors.New("upload: 会话已完成")
	// ErrChunkOutOfRange 分片编号越界（不在 1..N 内）。
	ErrChunkOutOfRange = errors.New("upload: 分片编号越界")
	// ErrInvalidChunkSize 分片大小不合规。
	ErrInvalidChunkSize = errors.New("upload: 分片大小不合规")
	// ErrIncompleteChunks 完成时存在缺片，随返回值给出完整缺片列表。
	ErrIncompleteChunks = errors.New("upload: 存在缺片，拒绝完成")
)

// Stats 是会话统计的快照。不变量：
// OverwriteCount == 成功上传数 - RegisteredChunks，
// TotalBytes == 当前各已登记分片大小之和。
type Stats struct {
	TotalChunks      int   // 创建时声明的总片数 N
	MaxChunkSize     int64 // 单片上限
	RegisteredChunks int   // 已登记的不同编号数
	TotalBytes       int64 // 当前各片大小之和
	OverwriteCount   int64 // 覆盖次数（同号再传的次数）
	Completed        bool  // 是否已完成（冻结）
}

// session 是单个会话的内部状态。
type session struct {
	totalChunks  int
	maxChunkSize int64
	chunks       map[int]int64 // 编号 -> 当前大小
	totalBytes   int64
	overwrites   int64
	uploads      int64 // 成功上传次数（含覆盖）
	completed    bool
}

// Manager 管理多个上传会话，所有方法可并发调用。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	nextID   int64
}

// NewManager 创建一个空的会话管理器。
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*session)}
}

// CreateSession 创建会话并返回会话 ID。totalChunks 与 maxChunkSize 必须为正。
func (m *Manager) CreateSession(totalChunks int, maxChunkSize int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if totalChunks <= 0 || maxChunkSize <= 0 {
		return "", ErrInvalidArgument
	}
	m.nextID++
	id := fmt.Sprintf("session-%d", m.nextID)
	m.sessions[id] = &session{
		totalChunks:  totalChunks,
		maxChunkSize: maxChunkSize,
		chunks:       make(map[int]int64),
	}
	return id, nil
}

// Upload 登记一个分片。同号再传为覆盖：字节数减旧加新，覆盖次数加一。
// 被拒绝时不改变任何统计。
func (m *Manager) Upload(sessionID string, chunkNumber int, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if s.completed {
		return ErrSessionCompleted
	}
	if chunkNumber < 1 || chunkNumber > s.totalChunks {
		return ErrChunkOutOfRange
	}
	if !validSize(s, chunkNumber, size) {
		return ErrInvalidChunkSize
	}
	if old, exists := s.chunks[chunkNumber]; exists {
		s.totalBytes += size - old
		s.overwrites++
	} else {
		s.totalBytes += size
	}
	s.chunks[chunkNumber] = size
	s.uploads++
	return nil
}

// Complete 尝试完成会话。若有缺片，按编号升序一次列全返回并拒绝，
// 已登记分片保留可补传；若齐全则冻结会话。重复完成返回 ErrSessionCompleted。
func (m *Manager) Complete(sessionID string) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if s.completed {
		return nil, ErrSessionCompleted
	}
	var missing []int
	for n := 1; n <= s.totalChunks; n++ {
		if _, ok := s.chunks[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		return missing, ErrIncompleteChunks
	}
	s.completed = true
	return nil, nil
}

// Query 返回会话统计快照。会话完成后仍如实返回。
func (m *Manager) Query(sessionID string) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return Stats{}, ErrSessionNotFound
	}
	return Stats{
		TotalChunks:      s.totalChunks,
		MaxChunkSize:     s.maxChunkSize,
		RegisteredChunks: len(s.chunks),
		TotalBytes:       s.totalBytes,
		OverwriteCount:   s.overwrites,
		Completed:        s.completed,
	}, nil
}

// ChunkSizes 返回当前各已登记分片的大小副本，供调用方核对账目。
func (m *Manager) ChunkSizes(sessionID string) (map[int]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	out := make(map[int]int64, len(s.chunks))
	for n, sz := range s.chunks {
		out[n] = sz
	}
	return out, nil
}

// validSize 校验分片大小：非末片必须等于上限，末片在 1..上限 之间。
func validSize(s *session, chunkNumber int, size int64) bool {
	if chunkNumber == s.totalChunks {
		return size >= 1 && size <= s.maxChunkSize
	}
	return size == s.maxChunkSize
}
