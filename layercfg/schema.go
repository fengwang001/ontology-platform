package layercfg

// IntRange 为整数键附加取值范围（闭区间）。
type IntRange struct {
	Min int64
	Max int64
}

// SchemaSpec 是登记键模式时的入参。
type SchemaSpec struct {
	Key      string
	Type     ValueType
	Required bool
	Range    *IntRange
	Merge    MergeMode
}

// validateSpec 校验模式登记自身的合法性。
func validateSpec(spec SchemaSpec) (Schema, error) {
	if spec.Key == "" {
		return Schema{}, errorf(ErrInvalidArgument, "schema key must not be empty")
	}
	switch spec.Type {
	case TypeString, TypeInt, TypeBool, TypeStringList:
	default:
		return Schema{}, errorf(ErrInvalidArgument, "key %q has unknown value type %d", spec.Key, spec.Type)
	}
	merge := spec.Merge
	if merge == 0 {
		merge = MergeReplace // 未指定时取默认值
	}
	switch merge {
	case MergeReplace:
	case MergeAppend:
		if spec.Type != TypeStringList {
			return Schema{}, errorf(ErrInvalidArgument, "key %q: append merge is only valid for string lists", spec.Key)
		}
	default:
		return Schema{}, errorf(ErrInvalidArgument, "key %q has unknown merge mode %d", spec.Key, merge)
	}
	if spec.Range != nil {
		if spec.Type != TypeInt {
			return Schema{}, errorf(ErrInvalidArgument, "key %q: integer range is only valid for int keys", spec.Key)
		}
		if spec.Range.Min > spec.Range.Max {
			return Schema{}, errorf(ErrInvalidArgument, "key %q: range min %d exceeds max %d", spec.Key, spec.Range.Min, spec.Range.Max)
		}
	}
	out := Schema{
		Key:      spec.Key,
		Type:     spec.Type,
		Required: spec.Required,
		Merge:    merge,
	}
	if spec.Range != nil {
		out.Min, out.Max, out.hasRange = spec.Range.Min, spec.Range.Max, true
	}
	return out, nil
}
