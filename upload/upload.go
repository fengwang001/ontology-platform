// Package upload 提供分片上传会话的登记与完成校验。
//
// 每个会话在创建时声明总片数 N 与单片大小上限，分片编号为 1..N。
// 非末片大小必须等于上限，末片大小在 1 到上限之间。同号分片再次上传
// 视为覆盖：总字节数减旧加新，覆盖次数加一。完成时若存在缺片，则按
// 编号升序一次性列出全部缺片并拒绝；已登记分片保留，可补传后再次完成。
// 完成成功后会话冻结，后续上传与重复完成均被拒绝，查询仍如实返回统计。
package upload

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrInvalidSessionParams 创建参数非正（总片数或单片上限 <= 0）。
	ErrInvalidSessionParams = errors.New("upload: invalid session params")
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = errors.New("upload: session not found")
	// ErrSessionCompleted 会话已完成（含重复完成与完成后上传）。
	ErrSessionCompleted = errors.New("upload: session already completed")
	// ErrPartOutOfRange 分片编号越界（不在 1..N 内）。
	ErrPartOutOfRange = errors.New("upload: part number out of range")
	// ErrInvalidPartSize 分片大小不合规。
	ErrInvalidPartSize = errors.New("upload: invalid part size")
	// ErrMissingParts 完成时存在缺片；错误消息按编号升序列出全部缺片。
	ErrMissingParts = errors.New("upload: missing parts")
)

// Stats 是会话统计的快照。
type Stats struct {
	TotalParts  int   // 创建时声明的总片数 N
	MaxPartSize int64 // 单片大小上限
	Registered  int   // 已登记的不同编号数
	Uploaded    int   // 成功上传次数（含覆盖）
	Overwrites  int   // 覆盖次数，恒等于 Uploaded - Registered
	Bytes       int64 // 当前各片大小之和
	Completed   bool  // 是否已完成（冻结）
	PartSizes   map[int]int64
}

// Registry 管理全部分片上传会话，可被并发调用。
type Registry struct {
	mu       sync.Mutex
	nextID   int
	sessions map[string]*session
}

type session struct {
	totalParts  int
	maxPartSize int64
	parts       map[int]int64
	uploaded    int
	overwrites  int
	bytes       int64
	completed   bool
}

// NewRegistry 返回一个空的上传会话登记器。
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]*session)}
}

// CreateSession 创建会话并返回会话 ID。totalParts 与 maxPartSize 必须为正。
func (r *Registry) CreateSession(totalParts int, maxPartSize int64) (string, error) {
	if totalParts <= 0 || maxPartSize <= 0 {
		return "", fmt.Errorf("%w: totalParts=%d maxPartSize=%d (均须为正)",
			ErrInvalidSessionParams, totalParts, maxPartSize)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := "sess-" + strconv.Itoa(r.nextID)
	r.sessions[id] = &session{
		totalParts:  totalParts,
		maxPartSize: maxPartSize,
		parts:       make(map[int]int64),
	}
	return id, nil
}

// UploadPart 登记一个分片。同号再传为覆盖。
// 多因同时成立时按「不存在、已完成、越界、大小」的固定优先级只报第一个。
func (r *Registry) UploadPart(sessionID string, partNumber int, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	if s.completed {
		return fmt.Errorf("%w: %q", ErrSessionCompleted, sessionID)
	}
	if partNumber < 1 || partNumber > s.totalParts {
		return fmt.Errorf("%w: part=%d 合法范围 1..%d",
			ErrPartOutOfRange, partNumber, s.totalParts)
	}
	if err := s.checkSize(partNumber, size); err != nil {
		return err
	}
	if old, exists := s.parts[partNumber]; exists {
		s.bytes += size - old
		s.overwrites++
	} else {
		s.bytes += size
	}
	s.parts[partNumber] = size
	s.uploaded++
	return nil
}

// checkSize 校验分片大小：非末片必须等于上限，末片在 1 到上限之间。
func (s *session) checkSize(partNumber int, size int64) error {
	if partNumber == s.totalParts {
		if size < 1 || size > s.maxPartSize {
			return fmt.Errorf("%w: 末片 part=%d size=%d 合法范围 1..%d",
				ErrInvalidPartSize, partNumber, size, s.maxPartSize)
		}
		return nil
	}
	if size != s.maxPartSize {
		return fmt.Errorf("%w: 非末片 part=%d size=%d 必须等于上限 %d",
			ErrInvalidPartSize, partNumber, size, s.maxPartSize)
	}
	return nil
}

// Complete 校验齐全并完成会话。缺片时按编号升序列出全部缺片并拒绝。
func (r *Registry) Complete(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	if s.completed {
		return fmt.Errorf("%w: %q (重复完成)", ErrSessionCompleted, sessionID)
	}
	var missing []string
	for p := 1; p <= s.totalParts; p++ {
		if _, ok := s.parts[p]; !ok {
			missing = append(missing, strconv.Itoa(p))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: 缺片编号(升序)=[%s]",
			ErrMissingParts, strings.Join(missing, ","))
	}
	s.completed = true
	return nil
}

// Stats 返回会话统计快照；会话不存在时返回 ErrSessionNotFound。
func (r *Registry) Stats(sessionID string) (Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return Stats{}, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	sizes := make(map[int]int64, len(s.parts))
	for p, sz := range s.parts {
		sizes[p] = sz
	}
	return Stats{
		TotalParts:  s.totalParts,
		MaxPartSize: s.maxPartSize,
		Registered:  len(s.parts),
		Uploaded:    s.uploaded,
		Overwrites:  s.overwrites,
		Bytes:       s.bytes,
		Completed:   s.completed,
		PartSizes:   sizes,
	}, nil
}
