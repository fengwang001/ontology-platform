package vv

// Registry 登记允许出现在版本向量中的副本 ID。
// 采用严格登记制：未登记 ID 出现在向量中即 ErrUnknownReplica。
type Registry struct {
	ids map[string]struct{}
}

// NewRegistry 用给定副本 ID 构造 registry。
func NewRegistry(ids ...string) *Registry {
	r := &Registry{ids: make(map[string]struct{}, len(ids))}
	for _, id := range ids {
		r.ids[id] = struct{}{}
	}
	return r
}

// Has 报告 id 是否已登记。
func (r *Registry) Has(id string) bool {
	_, ok := r.ids[id]
	return ok
}

// IDs 返回已登记副本数。
func (r *Registry) Len() int { return len(r.ids) }

// Register 追加一个副本 ID（已存在则无操作）。
func (r *Registry) Register(id string) { r.ids[id] = struct{}{} }

// Retire 注销一个副本 ID。
func (r *Registry) Retire(id string) { delete(r.ids, id) }

// Validate 检查 v 的所有分量 ID 均已登记。
func (r *Registry) Validate(v Vector) error {
	for id := range v {
		if !r.Has(id) {
			return ErrUnknownReplica
		}
	}
	return nil
}

// Increment 把 v 中 id 的计数器加一；达 uint64 上界返回 ErrOverflow，不回绕。
// 就地修改并返回 v。id 必须已登记。
func (r *Registry) Increment(v Vector, id string) (Vector, error) {
	if !r.Has(id) {
		return nil, ErrUnknownReplica
	}
	if v[id] == ^uint64(0) {
		return nil, ErrOverflow
	}
	if v == nil {
		v = Vector{}
	}
	v[id]++
	return v, nil
}

// MergeInto 把 src 按分量取最大值并入 dst（向量水位合并），返回 dst。
func MergeInto(dst, src Vector) Vector {
	if dst == nil {
		dst = Vector{}
	}
	for k, val := range src {
		if val > dst[k] {
			dst[k] = val
		}
	}
	return dst
}

// Rollback 检查 v 相对水位 water 是否有任何分量变小。
func Rollback(v, water Vector) bool {
	for k, val := range water {
		if v[k] < val {
			return true
		}
	}
	return false
}

// Project 返回 v 在 registry 已登记 ID 上的投影（用于裁剪后关系检查）。
func (r *Registry) Project(v Vector) Vector {
	out := Vector{}
	for id, val := range v {
		if r.Has(id) {
			out[id] = val
		}
	}
	return out
}

// ProjectSet 返回 v 删除 retired 中分量后的投影，不依赖 registry。
func ProjectSet(v Vector, retired map[string]struct{}) Vector {
	out := Vector{}
	for id, val := range v {
		if _, gone := retired[id]; !gone {
			out[id] = val
		}
	}
	return out
}
