// Package api 对外提供 chunked 增量快照导出。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/export"
	"ontology/snap"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrBadChunk   = errors.New("api: chunk size must be positive")
	ErrEmptyKey   = errors.New("api: empty key")
	ErrNoSnapshot = errors.New("api: no snapshot started")
	ErrFinished   = export.ErrFinished
)

// Handle 是一次 Snapshot 的句柄。
type Handle struct{ id int }

// DB 是实时状态加当前快照导出器，全部方法并发安全。
type DB struct {
	mu   sync.Mutex
	live map[string]int64
	exp  *export.Exporter
	size int
	seq  int
}

// New 建库；chunkSize <= 0 返回 ErrBadChunk，不产生任何状态。
func New(chunkSize int) (*DB, error) {
	if chunkSize <= 0 {
		return nil, ErrBadChunk
	}
	return &DB{live: make(map[string]int64), size: chunkSize}, nil
}

// Put 写入/覆盖；空 key 返回 ErrEmptyKey，状态不变。
func (db *DB) Put(key string, val int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.live[key] = val
	return nil
}

// Del 删除；空 key 返回 ErrEmptyKey，状态不变。
func (db *DB) Del(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.live, key)
	return nil
}

// Snapshot 持锁捕获点时刻副本（此后 Put/Del 不影响它）并返回句柄。
func (db *DB) Snapshot() Handle {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.seq++
	db.exp = export.New(snap.Capture(db.live), db.size)
	return Handle{id: db.seq}
}

// Next 导出下一块；未 Snapshot 返回 ErrNoSnapshot，
// 已导出完毕返回 ErrFinished，两者都不改变任何状态。
func (db *DB) Next(cursor string) ([]snap.Entry, string, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.exp == nil {
		return nil, "", false, ErrNoSnapshot
	}
	return db.exp.Next(cursor)
}

// Resume 等价于 Next：把上次的 newCursor 传回即可续传。
func (db *DB) Resume(cursor string) ([]snap.Entry, string, bool, error) {
	return db.Next(cursor)
}

// View 返回实时状态的有序副本。
func (db *DB) View() []snap.Entry {
	db.mu.Lock()
	defer db.mu.Unlock()
	s := snap.Capture(db.live)
	out := make([]snap.Entry, 0, s.Len())
	for i := 0; i < s.Len(); i++ {
		out = append(out, s.At(i))
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (db *DB) SelfCheck() error {
	fresh, err := New(2)
	if err != nil {
		return err
	}
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := fresh.Put(k, int64(i+1)); err != nil {
			return err
		}
	}
	fresh.Snapshot()
	if err := fresh.Put("d", 99); err != nil { // 快照后写，不影响导出
		return err
	}
	var all []snap.Entry
	cur := ""
	for {
		en, nc, done, err := fresh.Next(cur)
		if err != nil {
			return err
		}
		all = append(all, en...)
		cur = nc
		if done {
			break
		}
	}
	want := "[{a 1} {b 2} {c 3} {d 4} {e 5} {f 6}]"
	if fmt.Sprint(all) != want { // I1 拼接==全量、I2 无重复遗漏、I3 点时刻一致
		return fmt.Errorf("selfcheck: got %v want %s", all, want)
	}
	if _, _, _, err := fresh.Next(cur); !errors.Is(err, ErrFinished) { // I4 完成后拒绝
		return fmt.Errorf("selfcheck: want ErrFinished, got %v", err)
	}
	if _, _, _, err := new(DB).Next(""); !errors.Is(err, ErrNoSnapshot) {
		return fmt.Errorf("selfcheck: want ErrNoSnapshot, got %v", err)
	}
	if !export.CheckProbes() {
		return errors.New("selfcheck: probe count grows with n")
	}
	return nil
}
