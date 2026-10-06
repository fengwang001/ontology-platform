// Package part 维护按整数分区号组织的分区状态、版本与完整水位 W。
//
// 分区号 p 为 0 到 MaxPart 的整数，状态为 Missing（版本 0）或
// Committed（版本 >= 1）。版本只增不减。完整水位 W 是最大的 w 使
// 0 到 w 全部 Committed，不存在时为 -1；W 只增不减。
package part

import "errors"

// MaxPart 是最大分区号。
const MaxPart = 1_000_000

// ErrAlready 表示分区已处于 Committed 状态。
var ErrAlready = errors.New("part: partition already committed")

// Table 记录每个已提交分区的版本与完整水位。
// 零值不可用，请用 NewTable。Table 自身不加锁，同步由调用方负责。
type Table struct {
	vers   map[int]int // 仅含已提交分区；缺省为 Missing(0)
	w      int
	probes int // 最近一次 Commit/BumpRange 推进 W 时探测的分区数
}

// NewTable 返回初始 W = -1 的空表。
func NewTable() *Table {
	return &Table{vers: make(map[int]int), w: -1}
}

// Ver 返回分区当前版本，Missing 为 0。
func (t *Table) Ver(p int) int { return t.vers[p] }

// Committed 报告分区是否已提交。
func (t *Table) Committed(p int) bool {
	_, ok := t.vers[p]
	return ok
}

// W 返回完整水位。
func (t *Table) W() int { return t.w }

// Probes 返回最近一次 Commit/BumpRange 推进 W 时探测的分区数，
// 不超过 (新W - 旧W) + 1。
func (t *Table) Probes() int { return t.probes }

// Commit 实时提交：Missing 变为 Committed 且 ver=1；已 Committed 报 ErrAlready。
func (t *Table) Commit(p int) error {
	if t.Committed(p) {
		return ErrAlready
	}
	t.vers[p] = 1
	t.advance()
	return nil
}

// BumpRange 将 [a,b] 内每个分区版本加 1（Missing 视为 0，提交后为 1）。
func (t *Table) BumpRange(a, b int) {
	for p := a; p <= b; p++ {
		t.vers[p]++
	}
	t.advance()
}

// advance 从 w+1 起顺序探测并推进 W。
// 每次调用探测的分区数恰为 (新W - 旧W) + 1：推进经过的每个分区各一次，
// 外加第一个使推进停下的 Missing 分区。
func (t *Table) advance() {
	t.probes = 0
	for {
		t.probes++
		if _, ok := t.vers[t.w+1]; !ok {
			return
		}
		t.w++
	}
}
