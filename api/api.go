// Package api 对外提供快照隔离键值存储：写、快照、按快照读、释放与自检。依赖 snap。
package api

import (
	"errors"
	"fmt"

	"ontology/snap"
)

// 可判定的哨兵错误，四类互不相同。
var (
	ErrMaxSnapshots     = snap.ErrMaxSnapshots
	ErrEmptyKey         = snap.ErrEmptyKey
	ErrInvalidSnapshot  = snap.ErrInvalidSnapshot
	ErrTooManySnapshots = snap.ErrTooManySnapshots
	ErrSnapshotNotFound = snap.ErrSnapshotNotFound
)

// Store 是并发安全的快照隔离键值存储。
type Store struct{ s *snap.Store }

// New 构造 Store；maxSnapshots 非正返回 ErrMaxSnapshots。
func New(maxSnapshots int) (*Store, error) {
	s, err := snap.New(maxSnapshots)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}

// Write 追加一个版本；空 Key 返回 ErrEmptyKey 且不留痕。
func (st *Store) Write(key string, v int64) error { return st.s.Write(key, v) }

// Snapshot 固定当前版本为快照并登记；超限返回 ErrTooManySnapshots。
func (st *Store) Snapshot() (int, error) { return st.s.Snapshot() }

// Read 按快照版本取值；快照 id 非法返回 ErrInvalidSnapshot。
func (st *Store) Read(snap int, key string) (int64, error) { return st.s.Read(snap, key) }

// ReadCurrent 返回最新值（无则 0）。
func (st *Store) ReadCurrent(key string) int64 { return st.s.ReadCurrent(key) }

// Release 释放一个存活快照；id 非法或未登记返回对应哨兵错误。
func (st *Store) Release(snap int) error { return st.s.Release(snap) }

// SelfCheck 对内置操作序列核验四条不变量与 Read 的次线性定位。
func (st *Store) SelfCheck() error {
	if err := snap.SelfCheck(); err != nil {
		return err
	}
	s, err := New(1 << 20)
	if err != nil {
		return err
	}
	// 不变量 1：与朴素参照一致（重放 ver<=snap 的写，后写覆盖先写）。
	type w struct {
		ver int
		key string
		val int64
	}
	var log []w
	keys := []string{"a", "b", "c", "d", "e"}
	var snaps []int
	seed := uint64(513)
	next := func() uint64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 33 }
	for i := 0; i < 300; i++ {
		k, v := keys[next()%uint64(len(keys))], int64(next()%1000)
		if err := s.Write(k, v); err != nil {
			return err
		}
		sn, _ := s.Snapshot()
		log = append(log, w{sn, k, v})
		if i%17 == 0 {
			snaps = append(snaps, sn)
		}
	}
	naive := func(sn int, key string) int64 {
		var v int64
		for _, e := range log {
			if e.ver <= sn && e.key == key {
				v = e.val
			}
		}
		return v
	}
	for _, sn := range snaps {
		for _, k := range keys {
			got, err := s.Read(sn, k)
			if err != nil || got != naive(sn, k) {
				return fmt.Errorf("selfcheck: naive mismatch snap=%d key=%s", sn, k)
			}
		}
	}
	// 不变量 2：快照之后的写不影响该快照（跨键一致）。
	iso, _ := New(4)
	_ = iso.Write("x", 1)
	_ = iso.Write("y", 2)
	sn, _ := iso.Snapshot()
	_ = iso.Write("x", 10)
	_ = iso.Write("y", 20)
	vx, _ := iso.Read(sn, "x")
	vy, _ := iso.Read(sn, "y")
	if vx != 1 || vy != 2 {
		return errors.New("selfcheck: snapshot not isolated")
	}
	// 不变量 3：Read/Snapshot 不改变 ver 与已写值。
	before, _ := s.Snapshot()
	for _, k := range keys {
		_, _ = s.Read(before, k)
		_ = s.ReadCurrent(k)
	}
	after, _ := s.Snapshot()
	if before != after {
		return errors.New("selfcheck: reads mutated version")
	}
	// 不变量 4：被拒操作不留痕，且拒绝后仍可正常使用。
	cur := after
	_, e1 := s.Read(-1, "a")
	_, e2 := s.Read(cur+1, "a")
	rejects := []error{s.Write("", 1), e1, e2, s.Release(-1)}
	fresh, _ := New(2)
	_ = fresh.Write("k", 1)
	rejects = append(rejects, fresh.Release(1)) // id 合法但从未登记
	for _, e := range rejects {
		if e == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	if sn2, _ := s.Snapshot(); sn2 != cur {
		return errors.New("selfcheck: rejected op mutated version")
	}
	if v, _ := s.Read(cur, "a"); v != naive(cur, "a") {
		return errors.New("selfcheck: rejected op mutated history")
	}
	lim, _ := New(1)
	if _, err := lim.Snapshot(); err != nil {
		return err
	}
	if _, err := lim.Snapshot(); !errors.Is(err, ErrTooManySnapshots) {
		return errors.New("selfcheck: snapshot limit not enforced")
	}
	if err := lim.Release(0); err != nil {
		return err
	}
	if _, err := lim.Snapshot(); err != nil {
		return errors.New("selfcheck: store unusable after rejection")
	}
	return nil
}
