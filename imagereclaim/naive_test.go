package imagereclaim

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// naiveImg 是朴素模拟器中的镜像记录，字段语义与正式实现一一对应。
type naiveImg struct {
	layers   []Layer
	ready    bool
	lastUsed int64
	run      int
}

// naiveReclaimer 用最直白的 map 与“重新汇总”方式实现规则，作为对照真值。
type naiveReclaimer struct {
	c, k   int64
	maxNow int64
	imgs   map[string]*naiveImg
}

func newNaive(c int64, k int) *naiveReclaimer {
	return &naiveReclaimer{c: c, k: int64(k), imgs: make(map[string]*naiveImg)}
}

func (n *naiveReclaimer) usedNow() int64 {
	uniq := make(map[string]int64)
	for _, im := range n.imgs {
		for _, l := range im.layers {
			uniq[l.ID] = l.Size
		}
	}
	var sum int64
	for _, s := range uniq {
		sum += s
	}
	return sum
}

func naiveRepo(img string) string {
	if i := strings.LastIndexByte(img, ':'); i >= 0 {
		return img[:i]
	}
	return img
}

type naiveOutcome struct {
	errCode ErrCode
	layerID string
	gc      GCResult
}

func (n *naiveReclaimer) begin(img string, layers []Layer, now int64, autoCommit bool) naiveOutcome {
	badLayer := func(l Layer) bool { return l.ID == "" || l.Size < 1 || l.Size > 1_000_000_000_000 }
	if img == "" || len(layers) == 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	for _, l := range layers {
		if badLayer(l) {
			return naiveOutcome{errCode: ErrInvalidParam}
		}
	}
	for i := 0; i < len(layers); i++ {
		for j := i + 1; j < len(layers); j++ {
			if layers[i].ID == layers[j].ID {
				return naiveOutcome{errCode: ErrInvalidParam}
			}
		}
	}
	if now < 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	if now < n.maxNow {
		return naiveOutcome{errCode: ErrClockRewind}
	}
	if _, ok := n.imgs[img]; ok {
		return naiveOutcome{errCode: ErrImageExists}
	}
	sizes := n.layerSizes()
	for _, l := range layers {
		if s, ok := sizes[l.ID]; ok && s != l.Size {
			return naiveOutcome{errCode: ErrLayerConflict, layerID: l.ID}
		}
	}
	im := &naiveImg{layers: append([]Layer(nil), layers...), ready: autoCommit, lastUsed: 0}
	n.imgs[img] = im
	if autoCommit {
		im.lastUsed = now
	}
	if n.usedNow() > n.c {
		delete(n.imgs, img)
		return naiveOutcome{errCode: ErrNoSpace}
	}
	n.maxNow = now
	return naiveOutcome{}
}

func (n *naiveReclaimer) layerSizes() map[string]int64 {
	m := make(map[string]int64)
	for _, im := range n.imgs {
		for _, l := range im.layers {
			m[l.ID] = l.Size
		}
	}
	return m
}

func (n *naiveReclaimer) commit(img string, now int64) naiveOutcome {
	if img == "" || now < 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	if now < n.maxNow {
		return naiveOutcome{errCode: ErrClockRewind}
	}
	im, ok := n.imgs[img]
	if !ok {
		return naiveOutcome{errCode: ErrImageNotFound}
	}
	if im.ready {
		return naiveOutcome{errCode: ErrImageReady}
	}
	im.ready = true
	im.lastUsed = now
	n.maxNow = now
	return naiveOutcome{}
}

func (n *naiveReclaimer) abort(img string, now int64) naiveOutcome {
	if img == "" || now < 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	if now < n.maxNow {
		return naiveOutcome{errCode: ErrClockRewind}
	}
	im, ok := n.imgs[img]
	if !ok {
		return naiveOutcome{errCode: ErrImageNotFound}
	}
	if im.ready {
		return naiveOutcome{errCode: ErrImageReady}
	}
	delete(n.imgs, img)
	n.maxNow = now
	return naiveOutcome{}
}

func (n *naiveReclaimer) run(img string, now int64, stop bool) naiveOutcome {
	if img == "" || now < 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	if now < n.maxNow {
		return naiveOutcome{errCode: ErrClockRewind}
	}
	im, ok := n.imgs[img]
	if !ok {
		return naiveOutcome{errCode: ErrImageNotFound}
	}
	if !im.ready {
		return naiveOutcome{errCode: ErrImagePulling}
	}
	if stop && im.run == 0 {
		return naiveOutcome{errCode: ErrNotRunning}
	}
	if stop {
		im.run--
	} else {
		im.run++
	}
	im.lastUsed = now
	n.maxNow = now
	return naiveOutcome{}
}

func (n *naiveReclaimer) gc(now int64, high, low int, minAge int64) naiveOutcome {
	if high <= 0 || high > 100 || low <= 0 || low >= high || minAge < 0 || now < 0 {
		return naiveOutcome{errCode: ErrInvalidParam}
	}
	if now < n.maxNow {
		return naiveOutcome{errCode: ErrClockRewind}
	}
	n.maxNow = now

	used := n.usedNow()
	res := GCResult{}
	if used*100 < int64(high)*n.c {
		return naiveOutcome{gc: res}
	}
	need := used - (n.c*int64(low))/100

	repoOrder := make(map[string][]string)
	for id, im := range n.imgs {
		if im.ready {
			repoOrder[naiveRepo(id)] = append(repoOrder[naiveRepo(id)], id)
		}
	}
	prot := make(map[string]bool)
	for _, ids := range repoOrder {
		sort.Slice(ids, func(i, j int) bool {
			a, b := n.imgs[ids[i]], n.imgs[ids[j]]
			if a.lastUsed != b.lastUsed {
				return a.lastUsed > b.lastUsed
			}
			return ids[i] < ids[j]
		})
		lim := int(n.k)
		if lim > len(ids) {
			lim = len(ids)
		}
		for _, id := range ids[:lim] {
			prot[id] = true
		}
	}

	var cand []string
	for id, im := range n.imgs {
		if im.ready && im.run == 0 && !prot[id] && now-im.lastUsed >= minAge {
			cand = append(cand, id)
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		a, b := n.imgs[cand[i]], n.imgs[cand[j]]
		if a.lastUsed != b.lastUsed {
			return a.lastUsed < b.lastUsed
		}
		return cand[i] < cand[j]
	})

	for _, id := range cand {
		before := n.usedNow()
		delete(n.imgs, id)
		freed := before - n.usedNow()
		res.Deleted = append(res.Deleted, id)
		res.Freed += freed
		if res.Freed >= need {
			return naiveOutcome{gc: res}
		}
	}
	res.Short = res.Freed < need
	return naiveOutcome{gc: res}
}

// randomOp 是随机操作序列中的一条可重放操作。
type randomOp struct {
	kind   int
	img    string
	layers []Layer
	now    int64
	high   int
	low    int
	minAge int64
}

const (
	opBegin = iota
	opCommit
	opAbort
	opPull
	opRun
	opStop
	opGC
)

func generateOps(rng *rand.Rand) (int64, int, []randomOp) {
	c := int64(1 + rng.Intn(3000))
	k := rng.Intn(4)
	const repoN = 4
	const layerN = 8
	imgID := func() string { return fmt.Sprintf("repo%d:img%d", rng.Intn(repoN), rng.Intn(7)) }
	var ops []randomOp
	var now int64
	n := 40 + rng.Intn(120)
	for i := 0; i < n; i++ {
		// 大多数时刻单调不减，偶尔回退以覆盖时钟回退拒绝路径。
		if rng.Intn(7) != 0 {
			now += int64(rng.Intn(6))
		} else if now > 2 {
			now -= int64(1 + rng.Intn(2))
		}
		kind := rng.Intn(100)
		op := randomOp{kind: opPull, img: imgID(), now: now}
		switch {
		case kind < 30:
			op.kind = opBegin
		case kind < 42:
			op.kind = opPull
		case kind < 52:
			op.kind = opCommit
		case kind < 60:
			op.kind = opAbort
		case kind < 78:
			op.kind = opRun
		case kind < 90:
			op.kind = opStop
		default:
			op.kind = opGC
		}
		if op.kind == opBegin || op.kind == opPull {
			ln := 1 + rng.Intn(3)
			seen := map[string]bool{}
			for j := 0; j < ln; j++ {
				id := fmt.Sprintf("L%d", rng.Intn(layerN))
				if seen[id] {
					continue
				}
				seen[id] = true
				size := int64(1 + rng.Intn(700))
				if rng.Intn(20) == 0 {
					size = int64(1 + rng.Intn(1_000_000)) // 偶发大层，制造空间不足
				}
				op.layers = append(op.layers, Layer{ID: id, Size: size})
			}
			if len(op.layers) == 0 {
				op.layers = []Layer{{ID: "L0", Size: 1 + rng.Int63n(10)}}
			}
		}
		if op.kind == opGC {
			high := 30 + rng.Intn(71)
			low := 1 + rng.Intn(high-1)
			op.high, op.low = high, low
			op.minAge = int64(rng.Intn(8))
			if rng.Intn(15) == 0 {
				// 故意制造非法参数。
				switch rng.Intn(3) {
				case 0:
					op.high = 101
				case 1:
					op.low = op.high
				case 2:
					op.minAge = -1
				}
			}
		}
		ops = append(ops, op)
	}
	return c, k, ops
}
