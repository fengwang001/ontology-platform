package audit

// Category 是校验结论的类别。
type Category int

const (
	// OK 校验通过。
	OK Category = iota
	// ContentTampered 摘要重算与自报不符：内容被改。
	ContentTampered
	// SeqDiscontinuous 序号不等于位置：序号不连续。
	SeqDiscontinuous
	// ChainBroken 前摘要不等于上一条自报摘要（首条对创世值）：链断裂。
	ChainBroken
	// TailTruncated 链长小于最大锚点序号：尾部截断。
	TailTruncated
	// AnchorMismatch 锚点摘要与链中同序号记录自报摘要不符。
	AnchorMismatch
)

// String 返回类别的可读名称。
func (c Category) String() string {
	switch c {
	case OK:
		return "通过"
	case ContentTampered:
		return "内容被改"
	case SeqDiscontinuous:
		return "序号不连续"
	case ChainBroken:
		return "链断裂"
	case TailTruncated:
		return "尾部截断"
	case AnchorMismatch:
		return "与锚点不符"
	}
	return "未知"
}

// Result 是一次校验的结论。
type Result struct {
	OK             bool
	Category       Category
	Position       uint64 // 首个问题的 1 基位置；尾部截断为链长加一
	AnchoredPrefix uint64 // 通过时已被锚点确认的前缀长度（最大锚点序号）
}

// Verify 校验整条链并核对锚点。
// 校验在锁内对链与锚点取一致快照，因此看到的恒为某个自洽前缀。
func (v *Verifier) Verify() Result {
	v.mu.Lock()
	records := append([]Record(nil), v.records...)
	anchors := v.anchors.Snapshot()
	v.mu.Unlock()
	return v.verify(records, anchors)
}

// verify 分两段：先逐条检查链，再核对锚点；两段都有问题时报位置较小者，
// 位置相同取逐条检查的类别。
func (v *Verifier) verify(records []Record, anchors []Anchor) Result {
	// 第一段：逐条检查，取最小的问题位置。
	// 同一条记录多项成立时按固定优先级取第一个类别。
	var chainCat Category
	chainPos := uint64(0)
	prev := v.genesis
	for i, rec := range records {
		want := uint64(i) + 1
		var cat Category
		switch {
		case v.hash(rec.Seq, rec.Payload, rec.Prev) != rec.Digest:
			cat = ContentTampered
		case rec.Seq != want:
			cat = SeqDiscontinuous
		case rec.Prev != prev:
			cat = ChainBroken
		}
		if cat != OK {
			chainCat, chainPos = cat, want
			break
		}
		prev = rec.Digest
	}

	// 第二段：核对锚点。
	var anchorCat Category
	anchorPos := uint64(0)
	maxAnchor := uint64(0)
	if n := len(anchors); n > 0 {
		maxAnchor = anchors[n-1].Seq
	}
	if uint64(len(records)) < maxAnchor {
		anchorCat, anchorPos = TailTruncated, uint64(len(records))+1
	} else {
		for _, a := range anchors {
			if a.Seq == 0 || a.Seq > uint64(len(records)) {
				continue
			}
			if records[a.Seq-1].Digest != a.Digest {
				anchorCat, anchorPos = AnchorMismatch, a.Seq
				break
			}
		}
	}

	// 合并两段结果。
	switch {
	case chainPos == 0 && anchorPos == 0:
		return Result{OK: true, AnchoredPrefix: maxAnchor}
	case chainPos == 0:
		return Result{Category: anchorCat, Position: anchorPos}
	case anchorPos == 0:
		return Result{Category: chainCat, Position: chainPos}
	case chainPos <= anchorPos:
		return Result{Category: chainCat, Position: chainPos}
	default:
		return Result{Category: anchorCat, Position: anchorPos}
	}
}
