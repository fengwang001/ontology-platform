package snapshot

import "fmt"

// Validate 从 r 逐条读取记录并校验，返回完整结论或最大可恢复前缀。
//
// 校验不修改任何输入记录；函数本身无包级可变状态，可对不同的流并发
// 调用。同一个 Reader 实例表示一条单向游标，不应被多个校验并发共享。
func Validate(r Reader) Result {
	return validate(r, nil)
}

// ValidateSlice 是 Validate 针对内存切片的便捷封装。
func ValidateSlice(records []Record) Result {
	return Validate(NewSliceReader(records))
}

// validateStats 是仅供包内测试观察的内部度量。
type validateStats struct {
	// recordsRead 统计校验过程中实际通过 Reader 读取的记录数。
	recordsRead int
}

// validate 执行校验，并在 stats 非空时回填内部读取度量。
func validate(r Reader, stats *validateStats) Result {
	// objectTypes 只收录“已出现且自身字段合法”的对象记录：
	// id -> 对象类型。损坏的对象记录不会进入此表，因此后续链接引用它时
	// 按引用缺失处理。
	objectTypes := make(map[string]string)
	prefixLen := 0

	for {
		rec, err := r.Read()
		if err == ErrEnd {
			// 整个序列扫描完毕且每条记录均自洽。
			return Result{Status: StatusComplete, PrefixLen: prefixLen, BadIndex: -1, Reason: ReasonNone}
		}
		if stats != nil {
			stats.recordsRead++
		}
		if err != nil {
			// 读取下一条记录失败：无法取得可信字段的记录按其自身字段
			// 损坏处理，并依据判定次序归类为对象记录字段损坏
			// （KindUnknown 的记录无法被证明是链接记录）。
			return truncated(prefixLen, prefixLen, ReasonObjectCorrupt)
		}

		badIndex := prefixLen

		if rec.Kind == KindObject {
			id, typeName, ok := objectFields(rec)
			if !ok {
				return truncated(prefixLen, badIndex, ReasonObjectCorrupt)
			}
			if prev, exists := objectTypes[id]; exists && prev != typeName {
				// 同一对象标识重复声明且类型冲突：第二次出现自身损坏。
				return truncated(prefixLen, badIndex, ReasonObjectCorrupt)
			}
			// 同类型重复出现属于冗余，覆盖写入等价信息，无害。
			objectTypes[id] = typeName
			prefixLen++
			continue
		}

		if rec.Kind == KindLink {
			sourceID, targetID, _, ok := linkFields(rec)
			if !ok {
				// 判定次序：链接自身字段损坏优先于引用缺失。
				return truncated(prefixLen, badIndex, ReasonLinkCorrupt)
			}
			if _, ok1 := objectTypes[sourceID]; !ok1 {
				return truncated(prefixLen, badIndex, ReasonLinkReferenceMissing)
			}
			if _, ok2 := objectTypes[targetID]; !ok2 {
				return truncated(prefixLen, badIndex, ReasonLinkReferenceMissing)
			}
			prefixLen++
			continue
		}

		// Kind 缺失或非法：无法证明它是链接记录，按对象记录字段损坏
		// （最高优先级类别）归类。
		return truncated(prefixLen, badIndex, ReasonObjectCorrupt)
	}
}

func truncated(prefixLen, badIndex int, reason Reason) Result {
	return Result{
		Status:    StatusTruncated,
		PrefixLen: prefixLen,
		BadIndex:  badIndex,
		Reason:    reason,
	}
}

// objectFields 校验对象记录自身字段：id 与 type 必须存在且为非空字符串。
func objectFields(rec Record) (id, typeName string, ok bool) {
	if rec.ID == nil || rec.Type == nil {
		return "", "", false
	}
	if *rec.ID == "" || *rec.Type == "" {
		return "", "", false
	}
	return *rec.ID, *rec.Type, true
}

// linkFields 校验链接记录自身字段：source/target 标识、链接类型必须存在
// 且为非空字符串，方向必须合法。引用的先出现约束不在此函数内判定。
func linkFields(rec Record) (sourceID, targetID, linkType string, ok bool) {
	if rec.SourceID == nil || rec.TargetID == nil || rec.LinkType == nil {
		return "", "", "", false
	}
	if *rec.SourceID == "" || *rec.TargetID == "" || *rec.LinkType == "" {
		return "", "", "", false
	}
	if rec.Direction != DirForward && rec.Direction != DirReverse {
		return "", "", "", false
	}
	return *rec.SourceID, *rec.TargetID, *rec.LinkType, true
}

// String 返回结果的可读描述，便于审计日志记录。
func (r Result) String() string {
	switch r.Status {
	case StatusComplete:
		return fmt.Sprintf("complete(prefix=%d)", r.PrefixLen)
	default:
		return fmt.Sprintf("truncated(prefix=%d, badIndex=%d, reason=%s)", r.PrefixLen, r.BadIndex, r.Reason)
	}
}

// String 返回损坏原因分类的名称。
func (reason Reason) String() string {
	switch reason {
	case ReasonObjectCorrupt:
		return "object_record_corrupt"
	case ReasonLinkCorrupt:
		return "link_record_field_corrupt"
	case ReasonLinkReferenceMissing:
		return "link_reference_missing"
	default:
		return "none"
	}
}
