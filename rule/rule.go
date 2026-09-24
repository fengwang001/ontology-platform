// Package rule 定义条件 upsert 与墓碑的纯判定规则，不依赖其他包。
package rule

// Current 求键的当前版本：有存活行取行版本，否则有墓碑取墓碑版本，都没有为 0。
func Current(rowVer, tombVer int64, hasRow, hasTomb bool) int64 {
	if hasRow {
		return rowVer
	}
	if hasTomb {
		return tombVer
	}
	return 0
}

// ShouldApply 应用判定：仅当事件版本严格大于当前版本。
func ShouldApply(ver, cur int64) bool { return ver > cur }

// Expired 墓碑到期判定：G 与墓碑版本之差达到保留期 R 即到期。
func Expired(g, tombVer, r int64) bool { return g-tombVer >= r }
