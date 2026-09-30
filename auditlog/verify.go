package auditlog

import (
	"fmt"
	"sort"
)

// Category 是校验结论的类别。
type Category string

// 校验结论类别。
const (
	CatOK               Category = "通过"
	CatContentModified  Category = "内容被改"
	CatSeqDiscontinuity Category = "序号不连续"
	CatChainBroken      Category = "链断裂"
	CatTruncated        Category = "尾部截断"
	CatAnchorMismatch   Category = "与锚点不符"
)

// Result 是一次校验的结论。
type Result struct {
	OK       bool     // 是否通过
	Category Category // 问题类别；通过时为 CatOK
	Position uint64   // 首个问题位置（截断时为链长加一）；通过时为 0
	// AnchoredPrefix 是已被锚点确认的前缀长度（最大锚点序号），仅通过时有意义。
	AnchoredPrefix uint64
	Reason         string // 判定依据
}

// VerifyChain 是纯函数校验：对给定的链快照与锚点列表执行
// 逐条检查与锚点核对，返回首个问题的位置与类别。
//
// 判定顺序：
//  1. 逐条检查第 i 条（i 从 1 起），同条多项成立按此优先级：
//     摘要重算与自报不符为「内容被改」；序号不等于 i 为「序号不连续」；
//     前摘要不等于上一条自报摘要（首条对创世值）为「链断裂」。取最小的 i。
//  2. 锚点核对：链长小于最大锚点序号为「尾部截断」，报链长加一；
//     否则某锚点摘要与链中同序号记录的自报摘要不符为「与锚点不符」，
//     报该锚点序号（多个不符取最小序号）。
//  3. 两部分都有问题时报位置较小者；位置相同取逐条检查的类别。
//  4. 都无问题则通过，并报告已被锚点确认的前缀长度（最大锚点序号）。
func VerifyChain(records []Record, anchors []Anchor, fn DigestFunc) Result {
	if fn == nil {
		fn = DefaultDigestFunc
	}
	sorted := make([]Anchor, len(anchors))
	copy(sorted, anchors)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })

	recProblem, recOK := checkRecords(records, fn)
	anchorProblem, maxAnchor := checkAnchors(records, sorted)

	switch {
	case !recOK && anchorProblem != nil:
		if recProblem.Position <= anchorProblem.Position {
			return recProblem
		}
		return *anchorProblem
	case !recOK:
		return recProblem
	case anchorProblem != nil:
		return *anchorProblem
	}
	return Result{
		OK:             true,
		Category:       CatOK,
		AnchoredPrefix: maxAnchor,
		Reason:         fmt.Sprintf("链上 %d 条记录逐条检查通过，全部 %d 个锚点一致，已被锚点确认的前缀长度为 %d", len(records), len(sorted), maxAnchor),
	}
}

// checkRecords 执行逐条检查，返回首个问题；ok 为 false 时结果有效。
func checkRecords(records []Record, fn DigestFunc) (Result, bool) {
	prev := GenesisDigest
	for i, r := range records {
		pos := uint64(i) + 1
		switch {
		case r.Digest != fn(r.Seq, r.Payload, r.Prev):
			return Result{
				Category: CatContentModified,
				Position: pos,
				Reason:   fmt.Sprintf("第 %d 条记录的自报摘要与按（序号，负载，前摘要）重算的摘要不符", pos),
			}, false
		case r.Seq != pos:
			return Result{
				Category: CatSeqDiscontinuity,
				Position: pos,
				Reason:   fmt.Sprintf("第 %d 条记录的序号为 %d，序号不连续", pos, r.Seq),
			}, false
		case r.Prev != prev:
			return Result{
				Category: CatChainBroken,
				Position: pos,
				Reason:   fmt.Sprintf("第 %d 条记录的前摘要与上一条记录的自报摘要不符，链断裂", pos),
			}, false
		}
		prev = r.Digest
	}
	return Result{}, true
}

// checkAnchors 执行锚点核对，返回首个问题（无问题为 nil）与最大锚点序号。
func checkAnchors(records []Record, anchors []Anchor) (*Result, uint64) {
	var maxAnchor uint64
	if n := len(anchors); n > 0 {
		maxAnchor = anchors[n-1].Seq
	}
	if uint64(len(records)) < maxAnchor {
		return &Result{
			Category: CatTruncated,
			Position: uint64(len(records)) + 1,
			Reason:   fmt.Sprintf("链长 %d 小于最大锚点序号 %d，链尾部被截断", len(records), maxAnchor),
		}, maxAnchor
	}
	for _, a := range anchors {
		if a.Seq == 0 || a.Seq > uint64(len(records)) {
			continue
		}
		if records[a.Seq-1].Digest != a.Digest {
			return &Result{
				Category: CatAnchorMismatch,
				Position: a.Seq,
				Reason:   fmt.Sprintf("序号 %d 的锚点摘要与链中同序号记录的自报摘要不符", a.Seq),
			}, maxAnchor
		}
	}
	return nil, maxAnchor
}
