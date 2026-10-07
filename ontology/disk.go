package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound 表示磁盘上不存在该文件。
var ErrNotFound = errors.New("ontology: file not found")

// Disk 抽象出具有页缓存语义的持久存储：
//
//   - WriteFile/AppendFile 只写入易失缓冲（类似操作系统的页缓存）；
//   - Sync 才把某个文件的缓冲内容落盘，使其在崩溃后仍然可见；
//   - Crash 丢弃所有未 Sync 的缓冲内容，模拟进程/机器中断；
//   - Delete/Rename 视为元数据操作，立即持久（本模型不关心目录 fsync）。
//
// 提交记录的“写 + Sync”因此成为唯一、确定、可复现的原子判定时刻。
type Disk interface {
	WriteFile(name string, data []byte)
	AppendFile(name string, data []byte)
	ReadFile(name string) ([]byte, error)
	Sync(name string)
	Delete(name string)
	Rename(old, new string)
	Exists(name string) bool
	List(prefix string) []string
	// Crash 丢弃所有未 Sync 的缓冲内容，模拟一次中断。
	Crash()
}

// SimDisk 是 Disk 的确定性内存实现，用于实现与测试崩溃恢复语义。
type SimDisk struct {
	mu      sync.Mutex
	durable map[string][]byte
	pending map[string][]byte // 已写入但尚未 Sync 的内容（读路径可见）
}

// NewSimDisk 创建一个空的模拟磁盘。
func NewSimDisk() *SimDisk {
	return &SimDisk{
		durable: make(map[string][]byte),
		pending: make(map[string][]byte),
	}
}

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// WriteFile 将 data 写入易失缓冲；Sync 之前崩溃会丢失。
func (d *SimDisk) WriteFile(name string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[name] = clone(data)
}

// AppendFile 把 data 追加到文件当前可见内容之后（仍在缓冲中）。
func (d *SimDisk) AppendFile(name string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.pending[name]
	if cur == nil {
		cur = d.durable[name]
	}
	merged := make([]byte, 0, len(cur)+len(data))
	merged = append(merged, cur...)
	merged = append(merged, data...)
	d.pending[name] = merged
}

// ReadFile 返回进程当前可见的内容（缓冲优先，其次已持久内容）。
func (d *SimDisk) ReadFile(name string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.pending[name]; ok {
		return clone(b), nil
	}
	if b, ok := d.durable[name]; ok {
		return clone(b), nil
	}
	return nil, ErrNotFound
}

// Sync 把文件的缓冲内容落盘。
func (d *SimDisk) Sync(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.pending[name]; ok {
		d.durable[name] = clone(b)
		delete(d.pending, name)
	}
}

// Delete 立即删除文件（元数据操作，建模为即时持久）。
func (d *SimDisk) Delete(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending, name)
	delete(d.durable, name)
}

// Rename 立即重命名（元数据操作，建模为即时持久）。
func (d *SimDisk) Rename(old, new string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.pending[old]; ok {
		d.pending[new] = b
		delete(d.pending, old)
		return
	}
	if b, ok := d.durable[old]; ok {
		d.durable[new] = b
		delete(d.durable, old)
	}
}

// Exists 报告文件当前是否可见。
func (d *SimDisk) Exists(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pending[name]; ok {
		return true
	}
	_, ok := d.durable[name]
	return ok
}

// List 按字典序返回所有以 prefix 开头的可见文件名。
func (d *SimDisk) List(prefix string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := make(map[string]struct{})
	for name := range d.durable {
		if strings.HasPrefix(name, prefix) {
			seen[name] = struct{}{}
		}
	}
	for name := range d.pending {
		if strings.HasPrefix(name, prefix) {
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (d *SimDisk) Crash() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = make(map[string][]byte)
}
