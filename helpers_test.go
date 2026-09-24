package ontology

import (
	"fmt"
	"testing"
)

// numKeys 是搬迁与均衡测试使用的固定 key 数量。
const numKeys = 100_000

// genKeys 以固定方式生成 n 个 key，结果完全确定。
func genKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%08d", i)
	}
	return keys
}

// nodeIDs 生成 n 个节点 ID。
func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%02d", i)
	}
	return ids
}

// locateAll 对每个 key 逐个 Locate，返回归属切片。
func locateAll(t *testing.T, r *Ring, keys []string) []string {
	t.Helper()
	owners := make([]string, len(keys))
	for i, k := range keys {
		owner, err := r.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q) 出错: %v", k, err)
		}
		if owner == "" {
			t.Fatalf("Locate(%q) 返回空串", k)
		}
		owners[i] = owner
	}
	return owners
}

// distribution 统计每个节点承担的 key 数。
func distribution(owners []string) map[string]int {
	dist := make(map[string]int)
	for _, o := range owners {
		dist[o]++
	}
	return dist
}
