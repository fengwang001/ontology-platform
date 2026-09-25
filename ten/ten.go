// Package ten 实现单租户的独立命名空间、配额计数与配额判定。
// 本包不依赖任何其他包；并发安全由上层 store 串行化保证。
package ten

import "errors"

// 可判定的哨兵错误：空 key、key 不存在、两类配额超限。
var (
	ErrEmptyKey   = errors.New("ten: key must not be empty")
	ErrNotFound   = errors.New("ten: key not found")
	ErrQuotaKeys  = errors.New("ten: maxKeys quota exceeded")
	ErrQuotaBytes = errors.New("ten: maxBytes quota exceeded")
)

// Tenant 是单个租户的命名空间。计数器增量维护，绝不遍历重算。
type Tenant struct {
	data     map[string]string
	maxKeys  int
	maxBytes int

	keyCount   int
	totalBytes int

	// lastCheckedKeys 记录最近一次 Put 配额检查时检查过的 key 个数。
	// 非导出：只能被同包白盒测试读取，不进入任何公开接口。
	// 增量计数实现下只查目标 key 一个，故恒为 1，与租户已有 key 数无关。
	lastCheckedKeys int
}

// New 创建一个配额为 (maxKeys, maxBytes) 的空租户。
func New(maxKeys, maxBytes int) *Tenant {
	return &Tenant{
		data:     make(map[string]string),
		maxKeys:  maxKeys,
		maxBytes: maxBytes,
	}
}

// Put 写入或更新 key。判定顺序：空 key → 只查目标 key 取旧值 →
// 新 key 的 keyCount 配额 → 新总字节配额；全部通过后才变更状态，
// 因此任何拒绝路径都不留痕。
func (t *Tenant) Put(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	old, exists := t.data[key]
	t.lastCheckedKeys = 1 // 只检查目标 key，不遍历
	oldLen := 0
	if exists {
		oldLen = len(old)
	}
	if !exists && t.keyCount >= t.maxKeys {
		return ErrQuotaKeys
	}
	if t.totalBytes-oldLen+len(val) > t.maxBytes {
		return ErrQuotaBytes
	}
	if !exists {
		t.keyCount++
	}
	t.totalBytes += len(val) - oldLen
	t.data[key] = val
	return nil
}

// Get 返回 key 的值；空 key 返回 ErrEmptyKey，不存在返回 ErrNotFound。
func (t *Tenant) Get(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}
	val, ok := t.data[key]
	if !ok {
		return "", ErrNotFound
	}
	return val, nil
}

// Del 删除 key 并增量回收计数；空 key 返回 ErrEmptyKey，不存在返回 ErrNotFound。
func (t *Tenant) Del(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	old, ok := t.data[key]
	if !ok {
		return ErrNotFound
	}
	delete(t.data, key)
	t.keyCount--
	t.totalBytes -= len(old)
	return nil
}

// Usage 返回增量维护的 (keyCount, totalBytes)，O(1)。
func (t *Tenant) Usage() (keyCount, totalBytes int) {
	return t.keyCount, t.totalBytes
}

// Quota 返回当前 (maxKeys, maxBytes)。
func (t *Tenant) Quota() (maxKeys, maxBytes int) { return t.maxKeys, t.maxBytes }

// SetQuota 调整配额。
func (t *Tenant) SetQuota(maxKeys, maxBytes int) {
	t.maxKeys, t.maxBytes = maxKeys, maxBytes
}

// Snapshot 返回全部 key/val 的拷贝，供只读视图使用。
func (t *Tenant) Snapshot() map[string]string {
	cp := make(map[string]string, len(t.data))
	for k, v := range t.data {
		cp[k] = v
	}
	return cp
}

// Purge 清空租户全部数据并把计数归零。
func (t *Tenant) Purge() {
	t.data = make(map[string]string)
	t.keyCount, t.totalBytes = 0, 0
}
