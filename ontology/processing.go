package ontology

// validateChange 校验单条变更的静态合法性（不依赖当前状态）。
// 返回 RejectNone 表示通过；否则返回可区分的拒绝原因。
func validateChange(c Change) RejectReason {
	switch c.Kind {
	case KindAdd, KindRetract:
	default:
		return RejectInvalidArgument
	}
	if c.Row.Key == "" {
		return RejectInvalidArgument
	}
	return RejectNone
}

// topEntries 取有序条目的前 n 项（不足则全部），返回副本。
func topEntries(ordered []Entry, n int) []Entry {
	if len(ordered) < n {
		n = len(ordered)
	}
	return cloneEntries(ordered[:n])
}

// diffTopN 比较变更前后的前 N 名集合。
// before/after 为名次从 1 开始的完整有序条目（含榜外，Rank 已填好）。
// 返回的 left 按变更前名次升序、entered 按变更后名次升序；
// 调用方应先向日志/下游输出 left，再输出 entered。
func diffTopN(before, after []Entry, n int) (left, entered []Entry) {
	beforeTop := topEntries(before, n)
	afterTop := topEntries(after, n)

	wasIn := make(map[string]Entry, len(beforeTop))
	for _, e := range beforeTop {
		wasIn[e.Key] = e
	}
	nowIn := make(map[string]Entry, len(afterTop))
	for _, e := range afterTop {
		nowIn[e.Key] = e
	}

	// left：变更前在榜、变更后不在榜，按变更前名次升序。
	left = make([]Entry, 0)
	for _, e := range beforeTop {
		if _, ok := nowIn[e.Key]; !ok {
			left = append(left, e)
		}
	}

	// entered：变更后在榜、变更前不在榜，按变更后名次升序。
	entered = make([]Entry, 0)
	for _, e := range afterTop {
		if _, ok := wasIn[e.Key]; !ok {
			entered = append(entered, e)
		}
	}
	return left, entered
}

// cloneEntries 返回条目的拷贝（Entry 为值类型，切片拷贝即可），始终非 nil。
func cloneEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	copy(out, in)
	return out
}
