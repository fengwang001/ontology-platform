// Package runtimefilter 实现哈希连接的运行时过滤器协调器。
//
// 构建侧各分片并行上报连接键摘要（最小/最大值与去重键集合，或放弃），
// 全部分片到齐后合并出一份不可变的过滤器快照，供探测侧扫描按批使用，
// 以提前丢弃不可能匹配的行。过滤器只改变性能，不改变连接结果。
package runtimefilter

import "errors"

// JoinType 描述哈希连接的类型。
type JoinType int

const (
	JoinUnknown JoinType = iota
	JoinInner
	JoinLeftSemi
	JoinLeftAnti
	JoinLeftOuter
	JoinRightOuter
	JoinFullOuter
)

func (j JoinType) String() string {
	switch j {
	case JoinInner:
		return "inner"
	case JoinLeftSemi:
		return "left_semi"
	case JoinLeftAnti:
		return "left_anti"
	case JoinLeftOuter:
		return "left_outer"
	case JoinRightOuter:
		return "right_outer"
	case JoinFullOuter:
		return "full_outer"
	default:
		return "unknown"
	}
}

// 报告校验失败的可区分原因。
var (
	ErrUnknownJoinType = errors.New("runtimefilter: 连接类型未知，整体拒绝")
	ErrShardOutOfRange = errors.New("runtimefilter: 分片编号越界，整体拒绝")
	ErrDuplicateShard  = errors.New("runtimefilter: 同一分片重复报告，整体拒绝")
)

// ShardSummary 是构建侧单个分片上报的键摘要。
type ShardSummary struct {
	// Aborted 为 true 表示该分片放弃构建摘要，整个过滤器作废。
	Aborted bool
	// Empty 为 true 表示该分片没有任何构建行。
	Empty bool
	// Min、Max 为该分片键的最小、最大值；Empty 时忽略。
	Min int64
	Max int64
	// Keys 为该分片去重后的连接键集合；Empty 时忽略。
	Keys []int64
}

// Filter 是合并完成后的不可变过滤器快照。
// 一旦发布便不再修改，探测侧每一批只看到一份完整快照。
type Filter struct {
	// Invalid 为 true 表示某分片放弃，过滤器作废，所有行放行。
	Invalid bool
	// Empty 为 true 表示构建侧全部为空，内连接/半连接下探测行全部丢弃。
	Empty bool
	// Degraded 为 true 表示合并后去重数超过上限，只保留最小/最大值。
	Degraded bool
	Min      int64
	Max      int64
	Keys     map[int64]struct{}
}

// Match 判断非空键 key 是否可能匹配。键为空的行由调用方先行丢弃。
func (f *Filter) Match(key int64) bool {
	if f.Invalid {
		return true
	}
	if f.Empty {
		return false
	}
	if key < f.Min || key > f.Max {
		return false
	}
	if !f.Degraded {
		if _, ok := f.Keys[key]; !ok {
			return false
		}
	}
	return true
}

// merge 合并 N 个分片摘要为一份过滤器快照。
// 任一分片放弃则过滤器作废；合并后去重数超过上限则降级为只保留
// 最小/最大值；全部分片为空则得到 Empty 过滤器。
func merge(summaries []ShardSummary, maxDistinct int) *Filter {
	f := &Filter{}
	empty := true
	keys := make(map[int64]struct{})
	for _, s := range summaries {
		if s.Aborted {
			return &Filter{Invalid: true}
		}
		if s.Empty {
			continue
		}
		if empty {
			f.Min, f.Max = s.Min, s.Max
			empty = false
		} else {
			if s.Min < f.Min {
				f.Min = s.Min
			}
			if s.Max > f.Max {
				f.Max = s.Max
			}
		}
		for _, k := range s.Keys {
			keys[k] = struct{}{}
		}
	}
	if empty {
		f.Empty = true
		return f
	}
	if len(keys) > maxDistinct {
		f.Degraded = true
		return f
	}
	f.Keys = keys
	return f
}
