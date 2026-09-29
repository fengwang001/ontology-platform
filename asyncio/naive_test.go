package asyncio

// 本文件实现“朴素判定”参考模型：完全用直白的数据结构（不与生产代码共享任何
// 内部类型），按操作序列逐条重放，给出每个操作的接受/拒绝结果以及截至该步的
// 全部输出。测试再用生产 Buffer 重放同一序列，要求逐操作、逐条输出完全一致。

type naiveKind int

const (
	nvElement naiveKind = iota
	nvWatermark
)

type naiveItem struct {
	kind      naiveKind
	id        string
	value     any
	wm        int64
	seq       int
	completed bool
	emitted   bool
	seg       int
	doneAt    int
}

type naiveModel struct {
	mode      Mode
	capacity  int
	seq       int
	doneOrder int
	inFlight  int
	items     []*naiveItem
	lastWM    int64
	haveWM    bool
	pushSeg   int
	openSeg   int
	outputs   []Event
}

func newNaive(mode Mode, capacity int) *naiveModel {
	return &naiveModel{mode: mode, capacity: capacity}
}

type naiveResult struct {
	accepted bool
	err      error
}

// apply 执行一步操作（op: "push"/"wm"/"done"），返回是否接受。
func (n *naiveModel) apply(op string, id string, val any, wm int64) naiveResult {
	switch op {
	case "push":
		if id == "" {
			return naiveResult{err: ErrEmptyID}
		}
		for _, it := range n.items {
			if it.kind == nvElement && it.id == id {
				return naiveResult{err: ErrDuplicateID}
			}
		}
		if n.inFlight >= n.capacity {
			return naiveResult{err: ErrCapacityFull}
		}
		it := &naiveItem{
			kind:  nvElement,
			id:    id,
			value: val,
			seq:   n.seq,
			seg:   n.pushSeg,
		}
		n.seq++
		n.items = append(n.items, it)
		n.inFlight++
		n.pump()
		return naiveResult{accepted: true}
	case "wm":
		if n.haveWM && wm <= n.lastWM {
			return naiveResult{err: ErrWatermarkNotIncreasing}
		}
		pending := 0
		for _, it := range n.items {
			if it.kind == nvElement {
				pending++
			}
		}
		n.items = append(n.items, &naiveItem{
			kind: nvWatermark, wm: wm, seq: n.seq,
			// pending 用 items 动态推导，见 watermarkPending，无需保存。
		})
		_ = pending
		n.seq++
		n.lastWM = wm
		n.haveWM = true
		if n.mode == Unordered {
			n.pushSeg++
		}
		n.pump()
		return naiveResult{accepted: true}
	case "done":
		if id == "" {
			return naiveResult{err: ErrEmptyID}
		}
		var found *naiveItem
		for _, it := range n.items {
			if it.kind == nvElement && it.id == id {
				found = it
				break
			}
		}
		if found == nil {
			return naiveResult{err: ErrUnknownID}
		}
		if found.completed {
			return naiveResult{err: ErrAlreadyCompleted}
		}
		found.completed = true
		n.doneOrder++
		found.doneAt = n.doneOrder
		n.inFlight--
		n.pump()
		return naiveResult{accepted: true}
	}
	return naiveResult{}
}

// watermarkPending 朴素推导：该水位线之前（items 中排在它前面）、尚未输出的元素数。
func (n *naiveModel) watermarkPending(w *naiveItem) int {
	c := 0
	for _, it := range n.items {
		if it == w {
			break
		}
		if it.kind == nvElement && !it.emitted {
			c++
		}
	}
	return c
}

func (n *naiveModel) emit(it *naiveItem) {
	it.emitted = true
	if it.kind == nvElement {
		n.outputs = append(n.outputs, Event{
			Kind: KindElement, ID: it.id, Value: it.value, Seq: it.seq,
		})
	} else {
		n.outputs = append(n.outputs, Event{
			Kind: KindWatermark, Watermark: it.wm, Seq: it.seq,
		})
	}
}

// pump 朴素重放当前模式的输出判定，直到无法继续。
func (n *naiveModel) pump() {
	if n.mode == Ordered {
		for len(n.items) > 0 {
			h := n.items[0]
			if h.kind == nvElement {
				if !h.completed {
					return
				}
				n.emit(h)
				n.items = n.items[1:]
			} else {
				if n.watermarkPending(h) != 0 {
					return
				}
				n.emit(h)
				n.items = n.items[1:]
			}
		}
		return
	}

	// 无序：重复“开放段完成元素 -> 队头屏障放行”直到稳定。
	// openSeg 为已输出水位线数；段 k 开放当且仅当 k <= openSeg 等价于
	// 该元素前面没有尚未输出的水位线（k < openSeg 是它仍在 items 中的等价表述：
	// 若第 k 条水仍在 items，则该元素必排在它之后，无法成为 open 候选扫描项）。
	for {
		var best *naiveItem
		for _, it := range n.items {
			// 段 k 开放当且仅当已有 k 条水位线输出（openSeg >= k）；
			// 段 0 恒开放，被 seg <= openSeg 自然包含。
			if it.kind != nvElement || it.emitted || !it.completed || it.seg > n.openSeg {
				continue
			}
			if best == nil || it.doneAt < best.doneAt {
				best = it
			}
		}
		if best != nil {
			n.emit(best)
			n.remove(best)
			continue
		}
		if len(n.items) > 0 && n.items[0].kind == nvWatermark && n.watermarkPending(n.items[0]) == 0 {
			n.emit(n.items[0])
			n.items = n.items[1:]
			n.openSeg++
			// 屏障放行后重新扫描新开放段内早已完成、被扣住的元素。
			continue
		}
		return
	}
}

func (n *naiveModel) remove(target *naiveItem) {
	for i, it := range n.items {
		if it == target {
			n.items = append(n.items[:i], n.items[i+1:]...)
			return
		}
	}
}

// snapshot 返回输出副本。
func (n *naiveModel) snapshot() []Event {
	out := make([]Event, len(n.outputs))
	copy(out, n.outputs)
	return out
}
