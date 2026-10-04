// Package store 维护记录及其归属主体，提供按主体检索记录的索引。
// Store 不是并发安全的，调用方（erase.Executor）负责串行化。
package store

import (
	"errors"
	"slices"
)

const (
	MinID     int64 = 1
	MaxID     int64 = 1_000_000_000
	MaxOwners       = 4
	MinNow    int64 = 0
	MaxNow    int64 = 1_000_000_000_000
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRegression = errors.New("clock regression")
	ErrExists          = errors.New("already exists")
	ErrNotFound        = errors.New("not found")
	ErrTooManyOutgoing = errors.New("too many outgoing references")
)

func ValidID(id int64) bool { return id >= MinID && id <= MaxID }

func ValidNow(now int64) bool { return now >= MinNow && now <= MaxNow }

// ValidateOwners 校验 owners 为 0 到 4 个互不相同的非空主体名。
func ValidateOwners(owners []string) error {
	if len(owners) > MaxOwners {
		return ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(owners))
	for _, o := range owners {
		if o == "" {
			return ErrInvalidArgument
		}
		if _, dup := seen[o]; dup {
			return ErrInvalidArgument
		}
		seen[o] = struct{}{}
	}
	return nil
}

type Store struct {
	owners  map[int64]map[string]struct{}
	byOwner map[string]map[int64]struct{}
}

func New() *Store {
	return &Store{
		owners:  make(map[int64]map[string]struct{}),
		byOwner: make(map[string]map[int64]struct{}),
	}
}

// Add 登记记录；调用方需先完成时钟与墓碑检查。
func (s *Store) Add(id int64, owners []string) error {
	if !ValidID(id) || ValidateOwners(owners) != nil {
		return ErrInvalidArgument
	}
	if s.Exists(id) {
		return ErrExists
	}
	set := make(map[string]struct{}, len(owners))
	for _, o := range owners {
		set[o] = struct{}{}
	}
	s.owners[id] = set
	for _, o := range owners {
		bucket := s.byOwner[o]
		if bucket == nil {
			bucket = make(map[int64]struct{})
			s.byOwner[o] = bucket
		}
		bucket[id] = struct{}{}
	}
	return nil
}

func (s *Store) Exists(id int64) bool {
	_, ok := s.owners[id]
	return ok
}

func (s *Store) Len() int { return len(s.owners) }

// Owners 返回记录的归属主体（升序）；记录不存在时返回空切片。
func (s *Store) Owners(id int64) []string {
	set := s.owners[id]
	out := make([]string, 0, len(set))
	for o := range set {
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

// OwnedBy 返回归属某主体的全部记录 id（升序），供擦除取种子。
func (s *Store) OwnedBy(subject string) []int64 {
	set := s.byOwner[subject]
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// RemoveOwner 从记录中摘除一个主体；记录不存在或不归属该主体时为空操作。
func (s *Store) RemoveOwner(id int64, subject string) {
	set := s.owners[id]
	if set == nil {
		return
	}
	if _, ok := set[subject]; !ok {
		return
	}
	delete(set, subject)
	delete(s.byOwner[subject], id)
}

// Delete 删除记录并清理全部主体索引。
func (s *Store) Delete(id int64) {
	set := s.owners[id]
	for o := range set {
		delete(s.byOwner[o], id)
	}
	delete(s.owners, id)
}
