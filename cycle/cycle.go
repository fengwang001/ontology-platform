// Package cycle 负责为环形重命名生成不冲突的临时名。
package cycle

import (
	"errors"
	"strconv"
)

// ErrNoTempName 表示两套候选前缀族均被占用，无法取得安全临时名。
var ErrNoTempName = errors.New("cycle: no free temporary name available")

// TriesPerFamily 是每套前缀族尝试的候选数量。
const TriesPerFamily = 65536

// Prefix0、Prefix1 是两套候选前缀。
const (
	Prefix0 = ".rename-tmp-"
	Prefix1 = ".rename-tmp2-"
)

var prefixes = []string{Prefix0, Prefix1}

// Used 判断某个候选名是否被占用：现有名集合或本批目标名集合中存在即占用。
// 由调用方提供，便于注入与计数器统计。
type Used func(candidate string) bool

// TempName 返回一个不与现有名或任何目标名冲突的临时名。
// 冲突时按 前缀 + n（n 从 0 递增）重试，第一前缀族耗尽再换第二族；
// 两族均耗尽返回 ErrNoTempName（绝不返回会覆盖数据的名字）。
func TempName(isUsed Used) (string, error) {
	for _, prefix := range prefixes {
		for i := 0; i < TriesPerFamily; i++ {
			candidate := prefix + strconv.Itoa(i)
			if !isUsed(candidate) {
				return candidate, nil
			}
		}
	}
	return "", ErrNoTempName
}

// BuildNameSet 把现有名与目标名合并成一个用于占用校验的集合。
func BuildNameSet(existing []string, targets []string) map[string]struct{} {
	set := make(map[string]struct{}, len(existing)+len(targets))
	for _, s := range existing {
		set[s] = struct{}{}
	}
	for _, s := range targets {
		set[s] = struct{}{}
	}
	return set
}
