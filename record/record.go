// Package record 维护电子病历的文档版本链、哈希链、就诊与用户状态。
package record

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
)

// 角色常量。
const (
	RoleArchivist = "归档员"
	RoleMedical   = "医务审批"
	RoleRecords   = "病案审批"
)

// 文档状态。
const (
	StatusDraft    = 0 // 草稿
	StatusSigned   = 1 // 已签
	StatusCosigned = 2 // 已审签
)

var (
	ErrInvalid    = errors.New("record: invalid argument")
	ErrClock      = errors.New("record: clock moved backwards")
	ErrNotFound   = errors.New("record: not found")
	ErrPermission = errors.New("record: permission denied")
	ErrState      = errors.New("record: illegal state")
)

// Version 是文档的一个版本节点。
type Version struct {
	TS      int64
	Author  string
	Content string
	Hash    []byte
}

// Amendment 是封存后的补记节点。
type Amendment struct {
	TS      int64
	Author  string
	Content string
	Hash    []byte
}

// User 为系统用户。
type User struct {
	Name  string
	Dept  string
	Level int
	Roles map[string]bool
}

// Encounter 为一次就诊。
type Encounter struct {
	ID         string
	Discharge  int64
	Discharged bool
}

// Doc 为一份文档。
type Doc struct {
	ID          string
	Enc         string
	Author      string
	Status      int
	Vers        []Version
	Amends      []Amendment
	Sealed      bool
	Gen         int
	SealHash    []byte
	UnsealStart int64
	UnsealEnd   int64
	WindowOpen  bool
	SealTS      int64
	LastSealTS  int64
	SignTS      int64
	SignLate    bool
	CosignTS    int64
	CosignBy    string
	CosignLate  bool
}

// Store 是全部状态的持有者，并发安全。
type Store struct {
	T int64
	U int64

	mu     sync.Mutex
	hashes int
	maxNow int64
	users  map[string]*User
	encs   map[string]*Encounter
	docs   map[string]*Doc
}

// New 创建服务：T 为出院后自动封存时限，U 为解封窗口。
func New(T, U int64) *Store {
	if !validLimit(T) || !validLimit(U) {
		panic(ErrInvalid)
	}
	return &Store{
		T:      T,
		U:      U,
		users:  map[string]*User{},
		encs:   map[string]*Encounter{},
		docs:   map[string]*Doc{},
		maxNow: -1,
	}
}

func validTS(now int64) bool  { return now >= 0 && now <= 1_000_000_000 }
func validLimit(v int64) bool { return v >= 1 && v <= 1_000_000 }

// hashOnce 执行一次 SHA-256 并计数。
func (s *Store) hashOnce(prev []byte, ts int64, author, content string) []byte {
	s.hashes++
	return nodeHash(prev, ts, author, content)
}

// sealHashOnce 计算 SHA256(h_n ‖ 8字节大端 gen)。
func (s *Store) sealHashOnce(head []byte, gen int) []byte {
	s.hashes++
	h := sha256.New()
	h.Write(head)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(gen))
	h.Write(buf[:])
	return h.Sum(nil)
}

func nodeHash(prev []byte, ts int64, author, content string) []byte {
	h := sha256.New()
	h.Write(prev)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(ts))
	h.Write(buf[:])
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(author)))
	h.Write(lb[:])
	h.Write([]byte(author))
	h.Write([]byte(content))
	return h.Sum(nil)
}
