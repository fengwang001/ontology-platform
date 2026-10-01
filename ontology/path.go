package ontology

import (
	"sort"
	"strings"
)

const (
	maxDimensionsPerDoc = 16
	maxValuesPerDim     = 32
	maxPathSegments     = 4
	minTopN             = 1
	maxTopN             = 50
)

// isValidPath 判定 value 是否为合法路径：以 "/" 分隔的 1 到 4 段，
// 每段非空，因此不得以 "/" 开头或结尾，也不得含空段（"//"）。
func isValidPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	var segmentCount int
	start := 0
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == '/' {
			if start == i {
				return false
			}
			segmentCount++
			if segmentCount > maxPathSegments {
				return false
			}
			start = i + 1
		}
	}
	return segmentCount >= 1
}

// prefixNodes 返回路径的全部按段前缀（含路径本身）。
// 例如 "a/b/c" -> ["a", "a/b", "a/b/c"]。调用方须保证 value 合法。
func prefixNodes(value string) []string {
	nodes := make([]string, 0, maxPathSegments)
	for i := 0; i < len(value); i++ {
		if value[i] == '/' {
			nodes = append(nodes, value[:i])
		}
	}
	nodes = append(nodes, value)
	return nodes
}

// documentNodes 将 attrs 展开为“维度 -> 该文档在该维度上的节点集合”。
// 节点集合为全部值的全部前缀路径的并集，同一节点只保留一次。
// 调用前 attrs 已通过 validateAttrs 校验。
func documentNodes(attrs map[string][]string) map[string]map[string]struct{} {
	nodes := make(map[string]map[string]struct{}, len(attrs))
	for dim, values := range attrs {
		set := make(map[string]struct{})
		for _, value := range values {
			for _, node := range prefixNodes(value) {
				set[node] = struct{}{}
			}
		}
		nodes[dim] = set
	}
	return nodes
}

// validateAttrs 按题面顺序校验 attrs：
// 维度名为空、值非法、同一维度内值重复、维度或值数量超限。
// 为使拒绝原因可精确复现，按维度名、值的字节序依次检查。
func validateAttrs(attrs map[string][]string) error {
	dims := make([]string, 0, len(attrs))
	for dim := range attrs {
		dims = append(dims, dim)
	}
	sort.Strings(dims)
	// 按题面顺序：维度名为空 -> 值非法 -> 同维度值重复 -> 数量超限。
	for _, dim := range dims {
		if dim == "" {
			return ErrInvalidArgument
		}
	}
	for _, dim := range dims {
		values := attrs[dim]
		for _, value := range values {
			if !isValidPath(value) {
				return ErrInvalidArgument
			}
		}
	}
	for _, dim := range dims {
		values := attrs[dim]
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if _, dup := seen[value]; dup {
				return ErrInvalidArgument
			}
			seen[value] = struct{}{}
		}
	}
	if len(attrs) > maxDimensionsPerDoc {
		return ErrInvalidArgument
	}
	for _, dim := range dims {
		if len(attrs[dim]) > maxValuesPerDim {
			return ErrInvalidArgument
		}
	}
	return nil
}
