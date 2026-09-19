package ontology

import "time"

// Instance 是一个对象实例的快照。Version 从 1 开始单调递增，
// UpdatedAt 是最后一次写入时间。Properties 为深拷贝，调用方
// 修改它不会影响存储内部状态。
type Instance struct {
	ObjectType string
	PrimaryKey string
	Version    int64
	UpdatedAt  time.Time
	Properties map[string]any
}

// clone 返回实例的独立副本。
func (inst *Instance) clone() *Instance {
	return &Instance{
		ObjectType: inst.ObjectType,
		PrimaryKey: inst.PrimaryKey,
		Version:    inst.Version,
		UpdatedAt:  inst.UpdatedAt,
		Properties: cloneProps(inst.Properties),
	}
}

// cloneProps 深拷贝属性 map，递归处理嵌套的 map 与切片。
func cloneProps(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for key, val := range props {
		out[key] = cloneValue(val)
	}
	return out
}

func cloneValue(val any) any {
	switch typed := val.(type) {
	case map[string]any:
		return cloneProps(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		copy(out, typed)
		return out
	case []int:
		out := make([]int, len(typed))
		copy(out, typed)
		return out
	default:
		return val
	}
}

// OpKind 是批量写中单个操作的类型。
type OpKind int

const (
	OpCreate OpKind = iota
	OpUpdate
	OpDelete
)

// WriteOp 描述批量写中的一条操作。
//   - OpCreate：Properties 为初始属性，主键已存活时报 AlreadyExistsError，
//     主键处于逻辑删除状态时按复活处理（版本延续递增）。
//   - OpUpdate：必须带 ExpectedVersion。
//   - OpDelete：必须带 ExpectedVersion，逻辑删除。
type WriteOp struct {
	Kind            OpKind
	ObjectType      string
	PrimaryKey      string
	ExpectedVersion int64
	Properties      map[string]any
}
