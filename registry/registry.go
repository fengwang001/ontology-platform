// Package registry 维护桶元数据注册表。
//
// 记录每个桶的属主、字节数、开启上传数与对象记录数，
// 以及每个租户拥有的桶数。本包只提供线程安全的原语，
// 拒绝次序与业务规则由上层 xfer 包实现。
package registry

import "sync"

// Bucket 是单个桶的元数据。
type Bucket struct {
	Owner   string // 属主租户
	Bytes   int64  // 桶字节数
	Open    int64  // 开启中的上传数
	Objects int64  // 对象记录数（仅用于证明 Accept 不触碰对象）
}

// Registry 是桶元数据注册表。零值不可用，请使用 New。
type Registry struct {
	mu      sync.Mutex
	maxB    int
	buckets map[string]*Bucket
	counts  map[string]int
}

// New 返回空注册表，maxB 为每租户最多拥有的桶数。
func New(maxB int) *Registry {
	return &Registry{
		maxB:    maxB,
		buckets: make(map[string]*Bucket),
		counts:  make(map[string]int),
	}
}

// MaxB 返回每租户最多拥有的桶数。
func (r *Registry) MaxB() int {
	return r.maxB
}

// Has 报告桶是否存在。
func (r *Registry) Has(bucket string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.buckets[bucket]
	return ok
}

// Get 返回桶元数据副本；不存在时 ok 为 false。
func (r *Registry) Get(bucket string) (b Bucket, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bp, ok := r.buckets[bucket]
	if !ok {
		return Bucket{}, false
	}
	return *bp, true
}

// Count 返回租户当前拥有的桶数。
func (r *Registry) Count(t string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[t]
}

// Create 登记新桶，属主为 t，租户桶数加一。调用方需保证桶不存在。
func (r *Registry) Create(t, bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[bucket] = &Bucket{Owner: t}
	r.counts[t]++
}

// AddBytes 将桶字节数增加 d（d 可为负，调用方保证结果非负）。
func (r *Registry) AddBytes(bucket string, d int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[bucket].Bytes += d
}

// IncOpen 将桶的开启上传数加一。
func (r *Registry) IncOpen(bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[bucket].Open++
}

// DecOpen 将桶的开启上传数减一。调用方需保证计数大于 0。
func (r *Registry) DecOpen(bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[bucket].Open--
}

// IncObjects 将桶的对象记录数加 n。
func (r *Registry) IncObjects(bucket string, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[bucket].Objects += n
}

// Transfer 将桶的属主改为 to，并相应增减两租户的桶数。
// 不触碰桶的字节数、上传数与对象记录。
func (r *Registry) Transfer(bucket, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buckets[bucket]
	r.counts[b.Owner]--
	b.Owner = to
	r.counts[to]++
}

// Tenants 返回当前拥有至少一个桶的租户列表（无序）。
func (r *Registry) Tenants() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.counts))
	for t, c := range r.counts {
		if c > 0 {
			out = append(out, t)
		}
	}
	return out
}
