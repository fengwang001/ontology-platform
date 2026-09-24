// Package cycle 在重命名依赖的函数图上检测有向环并分配破环临时名。
//
// 每个旧名至多是一个请求的新名（否则属重复目标冲突），故 old→new
// 构成函数图，其中的有向环都是简单环。每个环只需拆一条边即可破环，
// 因此所需临时名数量恰好等于环的个数。
package cycle

import (
	"errors"
	"fmt"
	"sort"
)

// ErrNoTempName 表示临时名生成重试次数耗尽，属于可判定错误。
var ErrNoTempName = errors.New("cycle: 无法生成不冲突的临时名")

const maxAttempts = 1 << 20

// Find 在函数图 edges（old→new）中找出所有长度 ≥2 的有向环。
// 每个环按环序返回，并以环上字典序最小的旧名开头，保证确定性。
func Find(edges map[string]string) [][]string {
	done := make(map[string]bool, len(edges))
	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var cycles [][]string
	for _, start := range keys {
		if done[start] {
			continue
		}
		index := map[string]int{}
		var path []string
		cur := start
		for !done[cur] {
			if at, ok := index[cur]; ok {
				cycles = append(cycles, canonical(path[at:]))
				break
			}
			index[cur] = len(path)
			path = append(path, cur)
			next, ok := edges[cur]
			if !ok {
				break
			}
			cur = next
		}
		for _, s := range path {
			done[s] = true
		}
	}
	return cycles
}

// canonical 旋转环使其以字典序最小的元素开头。
func canonical(ring []string) []string {
	best := 0
	for i := range ring {
		if ring[i] < ring[best] {
			best = i
		}
	}
	out := make([]string, len(ring))
	for i := range ring {
		out[i] = ring[(best+i)%len(ring)]
	}
	return out
}

// TempNamer 生成不与现有名冲突的临时名，冲突时递增计数器重试。
type TempNamer struct {
	taken   func(string) bool
	counter int
}

// NewTempNamer 创建临时名生成器，taken 报告某名字是否已被占用。
func NewTempNamer(taken func(string) bool) *TempNamer {
	return &TempNamer{taken: taken}
}

// Next 返回下一个不冲突的临时名；重试耗尽返回 ErrNoTempName。
func (t *TempNamer) Next() (string, error) {
	for i := 0; i < maxAttempts; i++ {
		cand := fmt.Sprintf("~tmp-%d", t.counter)
		t.counter++
		if !t.taken(cand) {
			return cand, nil
		}
	}
	return "", ErrNoTempName
}
