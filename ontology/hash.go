package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// canonicalChanges 以实例标识排序，保证哈希输入确定、与 map 遍历顺序无关。
func canonicalChanges(changes []Change) []Change {
	out := make([]Change, len(changes))
	copy(out, changes)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// hashRecord 计算记录哈希。哈希链形如 H(prevHash || 规范字段)，
// 任一字段或顺序被改动都会使后续校验失败。
func hashRecord(rec *Record) string {
	var b strings.Builder
	b.WriteString(rec.PrevHash)
	fmt.Fprintf(&b, "|seq=%d|action=%q|kind=%d|outcome=%d|corrOf=%d|",
		rec.Seq, rec.ActionID, rec.Kind, rec.Outcome, rec.CorrectionOf)
	for _, c := range canonicalChanges(rec.Changes) {
		fmt.Fprintf(&b, "c=%q:%q->%q;", c.Instance, c.Before, c.After)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
