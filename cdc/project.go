package cdc

import (
	"fmt"
	"strconv"
)

// TraceAction 逐列投影动作的类别。
type TraceAction string

const (
	// ActionKeep 事件值与目标列类型一致，原样保留。
	ActionKeep TraceAction = "keep"
	// ActionConvert 事件值经类型转换后写入（int->string 或 string->int）。
	ActionConvert TraceAction = "convert"
	// ActionZeroFill 事件缺失该列且列非必填，补该类型零值。
	ActionZeroFill TraceAction = "zero-fill"
	// ActionDrop 事件字段在目标模式中已删除，静默丢弃。
	ActionDrop TraceAction = "drop"
	// ActionReject 该列导致整个事件被拒收。
	ActionReject TraceAction = "reject"
)

// ColumnTrace 单列投影的判定记录，用于日志与人工核对。
type ColumnTrace struct {
	Column string
	Action TraceAction
	In     any    // 事件中的原始值（丢弃/缺失时为 nil）
	Out    any    // 写入投影结果的值（拒收/丢弃时为 nil）
	Reason string // 判定依据，例如 "int->string always succeeds"
}

// Projection 一次投影的完整结果：Values 为投影后的事件字段，
// Trace 按目标模式列序（随后是被丢弃字段）排列的逐列判定记录。
type Projection struct {
	Values map[string]any
	Trace  []ColumnTrace
}

// Project 将事件投影到目标版本模式，仅返回投影后的字段。
func (r *Registry) Project(e Event, target int) (map[string]any, error) {
	p, err := r.ProjectWithTrace(e, target)
	if err != nil {
		return nil, err
	}
	return p.Values, nil
}

// ProjectWithTrace 与 Project 相同，但额外返回逐列判定记录。
// 投影基于目标版本的一次性不可变快照，期间发生的模式演进
// 不会影响本次投影，因此不会出现新旧模式混合。
func (r *Registry) ProjectWithTrace(e Event, target int) (Projection, error) {
	schema, ok := r.Get(target)
	if !ok {
		return Projection{}, fmt.Errorf("%w: %d", ErrVersionNotFound, target)
	}
	return project(schema, e)
}

// project 在已取得的模式快照上执行投影，纯函数、结果确定。
func project(schema Schema, e Event) (Projection, error) {
	values := make(map[string]any, len(schema.Columns))
	trace := make([]ColumnTrace, 0, len(schema.Columns))

	for _, col := range schema.Columns {
		raw, present := e.Fields[col.Name]
		if !present {
			if col.Required {
				err := fmt.Errorf("%w: %q", ErrMissingRequired, col.Name)
				trace = append(trace, ColumnTrace{
					Column: col.Name,
					Action: ActionReject,
					Reason: fmt.Sprintf("required %s column absent from event", col.Type),
				})
				return Projection{Trace: trace}, err
			}
			zero := col.Type.Zero()
			values[col.Name] = zero
			trace = append(trace, ColumnTrace{
				Column: col.Name,
				Action: ActionZeroFill,
				Out:    zero,
				Reason: fmt.Sprintf("optional column absent, filled with %s zero value", col.Type),
			})
			continue
		}

		out, note, err := convert(raw, col)
		if err != nil {
			trace = append(trace, ColumnTrace{
				Column: col.Name,
				Action: ActionReject,
				In:     raw,
				Reason: err.Error(),
			})
			return Projection{Trace: trace}, err
		}
		action := ActionKeep
		if note != "" {
			action = ActionConvert
		}
		values[col.Name] = out
		trace = append(trace, ColumnTrace{
			Column: col.Name,
			Action: action,
			In:     raw,
			Out:    out,
			Reason: note,
		})
	}

	// 目标模式中不存在的字段（已删除列）静默丢弃，按列名排序保证确定性。
	for _, name := range sortedKeys(e.Fields) {
		if _, kept := values[name]; kept {
			continue
		}
		trace = append(trace, ColumnTrace{
			Column: name,
			Action: ActionDrop,
			In:     e.Fields[name],
			Reason: "column not present in target schema, silently dropped",
		})
	}

	return Projection{Values: values, Trace: trace}, nil
}

// convert 把事件值对齐到目标列类型。
// 返回转换后的值与转换说明；同类型时说明为空串。
func convert(raw any, col Column) (any, string, error) {
	switch col.Type {
	case TypeInt:
		switch v := raw.(type) {
		case int:
			return int64(v), "", nil
		case int64:
			return v, "", nil
		case string:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, "", fmt.Errorf("%w: column %q value %q is not a valid integer", ErrConversionFailed, col.Name, v)
			}
			return n, "string->int parsed as base-10 integer", nil
		}
	case TypeString:
		switch v := raw.(type) {
		case string:
			return v, "", nil
		case int:
			return strconv.Itoa(v), "int->string always succeeds", nil
		case int64:
			return strconv.FormatInt(v, 10), "int->string always succeeds", nil
		}
	}
	return nil, "", fmt.Errorf("%w: column %q got %T", ErrInvalidValue, col.Name, raw)
}

// sortedKeys 返回排序后的键集合，保证丢弃列的追踪顺序确定。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
