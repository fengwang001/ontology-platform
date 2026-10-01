package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type blockRange struct {
	lo []byte // 块最小键（第一块可任意，后续块由上一块 next 给定）
	hi []byte // 块最大键
}

// genOrderedBlocks 生成 n 个满足 hi[i] < lo[i+1] 的块。
// 键取自一个有序随机键池，保证严格顺序。
func genOrderedBlocks(rng *rand.Rand, n int) []blockRange {
	pool := genSortedKeyPool(rng, 2*n+1+rng.Intn(2*n))
	blocks := make([]blockRange, n)
	for i := 0; i < n; i++ {
		lo := pool[2*i]
		hi := pool[2*i+1]
		if bytes.Compare(lo, hi) > 0 {
			lo, hi = hi, lo
		}
		blocks[i] = blockRange{lo: lo, hi: hi}
	}
	return blocks
}

// genSortedKeyPool 生成 cnt 个互不相同、按字节序排列的非空随机键。
func genSortedKeyPool(rng *rand.Rand, cnt int) [][]byte {
	seen := map[string]bool{}
	keys := make([][]byte, 0, cnt)
	for len(keys) < cnt {
		k := randKey(rng, 1+rng.Intn(4))
		if seen[string(k)] {
			continue
		}
		seen[string(k)] = true
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })
	return keys
}

func randKey(rng *rand.Rand, n int) []byte {
	k := make([]byte, n)
	for i := range k {
		// 只用少量取值，制造公共前缀与相邻字节等场景。
		k[i] = byte(rng.Intn(6)) // 0..5
	}
	return k
}

// sampleKeysInRange 生成（近似）落在 [lo, hi] 内的键样本：
// 包含边界本身，以及在 lo/hi 附近随机扰动但仍处于区间内的键。
func sampleKeysInRange(rng *rand.Rand, lo, hi []byte, max int) [][]byte {
	out := [][]byte{bytes.Clone(lo), bytes.Clone(hi)}
	seen := map[string]bool{string(lo): true, string(hi): true}
	for attempt := 0; len(out) < max && attempt < 200; attempt++ {
		base := hi
		k := bytes.Clone(base)
		pos := rng.Intn(len(k))
		k[pos] = byte(int(k[pos]) - rng.Intn(int(k[pos])+1))
		k = append(k[:pos+1], make([]byte, rng.Intn(3))...)
		if bytes.Compare(k, lo) < 0 || bytes.Compare(k, hi) > 0 || len(k) == 0 {
			continue
		}
		if seen[string(k)] {
			continue
		}
		seen[string(k)] = true
		out = append(out, k)
	}
	return out
}

func buildFromBlocks(t *testing.T, blocks []blockRange) *Builder {
	t.Helper()
	var b Builder
	for i := range blocks {
		if i < len(blocks)-1 {
			if _, err := b.AddBlock(blocks[i].hi, blocks[i+1].lo); err != nil {
				t.Fatalf("AddBlock(%q,%q): %v", blocks[i].hi, blocks[i+1].lo, err)
			}
		} else {
			if _, err := b.Finish(blocks[i].hi); err != nil {
				t.Fatalf("Finish(%q): %v", blocks[i].hi, err)
			}
		}
	}
	return &b
}

func blocksKey(blocks []blockRange) string {
	var w bytes.Buffer
	for _, blk := range blocks {
		fmt.Fprintf(&w, "%s:%s|", blk.lo, blk.hi)
	}
	return w.String()
}

// ---- 朴素参考实现（严格按题意逐条字节规则书写，不共享产品代码） ----

type naiveBuilder struct {
	seps     [][]byte
	prevNext []byte
	finished bool
}

func naiveMakeSep(last, next []byte) []byte {
	d := 0
	for d < len(last) && d < len(next) {
		if last[d] != next[d] {
			break
		}
		d++
	}
	if d == len(last) || d == len(next) {
		out := make([]byte, len(last))
		copy(out, last)
		return out
	}
	bb := last[d]
	if bb < 0xFF && bb+1 < next[d] {
		out := make([]byte, d+1)
		copy(out, last[:d])
		out[d] = bb + 1
		return out
	}
	out := make([]byte, len(last))
	copy(out, last)
	return out
}

func naiveFinishSep(last []byte) []byte {
	for i := 0; i < len(last); i++ {
		if last[i] != 0xFF {
			out := make([]byte, i+1)
			copy(out, last[:i])
			out[i] = last[i] + 1
			return out
		}
	}
	out := make([]byte, len(last))
	copy(out, last)
	return out
}

func (n *naiveBuilder) addBlock(last, next []byte) error {
	if len(last) == 0 || len(next) == 0 {
		return ErrEmptyKey
	}
	if bytes.Compare(last, next) >= 0 {
		return ErrLastNotBeforeNext
	}
	if n.prevNext != nil && bytes.Compare(last, n.prevNext) < 0 {
		return ErrOutOfOrderBlock
	}
	if n.finished {
		return ErrAlreadyFinished
	}
	n.seps = append(n.seps, naiveMakeSep(last, next))
	n.prevNext = bytes.Clone(next)
	return nil
}

func (n *naiveBuilder) finish(last []byte) error {
	if len(last) == 0 {
		return ErrEmptyKey
	}
	if n.prevNext != nil && bytes.Compare(last, n.prevNext) < 0 {
		return ErrOutOfOrderBlock
	}
	if n.finished {
		return ErrAlreadyFinished
	}
	n.seps = append(n.seps, naiveFinishSep(last))
	n.finished = true
	return nil
}

func (n *naiveBuilder) seek(key []byte) (int, error) {
	if !n.finished {
		return 0, ErrNotFinished
	}
	for i, sep := range n.seps {
		if bytes.Compare(sep, key) >= 0 {
			return i, nil
		}
	}
	return SeekOutOfBound, nil
}

// op 是一次随机操作。
type op struct {
	kind    int // 0=AddBlock 1=Finish 2=Seek 3=重复Finish/非法AddBlock
	last    []byte
	next    []byte
	key     []byte
	wantErr error
}

// TestNaiveDifferential 2000 组随机操作序列对拍：同一序列分别喂给
// 产品实现与朴素实现，逐字节比较分隔键、错误类型、Seek 结果。
func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	logged := 0
	for iter := 0; iter < 2000; iter++ {
		ops := genRandomOpSequence(rng, 1+rng.Intn(8))
		var got Builder
		var want naiveBuilder
		var log bytes.Buffer
		fmt.Fprintf(&log, "序列 %d: ", iter)
		for step, o := range ops {
			switch o.kind {
			case 0, 3:
				_, ge := got.AddBlock(o.last, o.next)
				we := want.addBlock(o.last, o.next)
				fmt.Fprintf(&log, "\n  [%d] AddBlock(last=%q,next=%q) -> err=%v",
					step, hexKey(o.last), hexKey(o.next), ge)
				if !sameErr(ge, we) {
					t.Fatalf("iter %d step %d AddBlock err: got=%v want=%v\n输入:%s",
						iter, step, ge, we, log.String())
				}
				if ge == nil {
					gs, ws := got.Seps(), append([][]byte(nil), want.seps...)
					if !sepsEqual(gs, ws) {
						t.Fatalf("iter %d step %d seps 不一致:\n got=%q\nwant=%q",
							iter, step, gs, ws)
					}
				}
			case 1:
				_, ge := got.Finish(o.last)
				we := want.finish(o.last)
				fmt.Fprintf(&log, "\n  [%d] Finish(last=%q) -> err=%v",
					step, hexKey(o.last), ge)
				if !sameErr(ge, we) {
					t.Fatalf("iter %d step %d Finish err: got=%v want=%v\n输入:%s",
						iter, step, ge, we, log.String())
				}
			case 2:
				gi, gerr := got.Seek(o.key)
				wi, werr := want.seek(o.key)
				fmt.Fprintf(&log, "\n  [%d] Seek(key=%q) -> (%d,%v)",
					step, hexKey(o.key), gi, gerr)
				if !sameErr(gerr, werr) || gi != wi {
					t.Fatalf("iter %d step %d Seek: got=(%d,%v) want=(%d,%v)\n输入:%s",
						iter, step, gi, gerr, wi, werr, log.String())
				}
			}
		}
		gs, ws := got.Seps(), append([][]byte(nil), want.seps...)
		fmt.Fprintf(&log, "\n  最终 seps: got=%q want=%q | 判定依据: 逐字节相等", gs, ws)
		if !sepsEqual(gs, ws) {
			t.Fatalf("iter %d 最终 seps 不一致:\n got=%q\nwant=%q", iter, gs, ws)
		}
		// 确定性重放：同一序列再来一遍，必须逐字节相同。
		var replay Builder
		for _, o := range ops {
			switch o.kind {
			case 0, 3:
				if _, err := replay.AddBlock(o.last, o.next); err != nil {
					break
				}
			case 1:
				_, _ = replay.Finish(o.last)
			}
		}
		if !sepsEqual(replay.Seps(), gs) {
			t.Fatalf("iter %d 重放结果不一致", iter)
		}
		if logged < 5 {
			t.Log(log.String())
			logged++
		}
	}
	t.Log("完成 2000 组随机序列对拍（含确定性重放）；上面打印前 5 组的输入/输出/判定依据")
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func sepsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// genRandomOpSequence 生成随机操作序列：先以有序键池构造若干合法 AddBlock，
// 再插入各种非法操作与 Finish 前/后的 Seek。
func genRandomOpSequence(rng *rand.Rand, maxBlocks int) []op {
	pool := genSortedKeyPool(rng, 2+2*maxBlocks)
	var ops []op
	n := 1 + rng.Intn(maxBlocks)

	// Finish 前 Seek（朴素与产品都应报 ErrNotFinished）。
	if rng.Intn(3) == 0 {
		ops = append(ops, op{kind: 2, key: pool[0]})
	}

	for i := 0; i < n; i++ {
		last := pool[2*i]
		// 随机插入非法操作。
		switch rng.Intn(6) {
		case 0:
			ops = append(ops, op{kind: 3, last: nil, next: pool[2*i+1]}) // 空键
		case 1:
			ops = append(ops, op{kind: 3, last: pool[2*i+1], next: last}) // last>next
		}
		if i < n-1 {
			ops = append(ops, op{kind: 0, last: last, next: pool[2*i+2]})
			if rng.Intn(6) == 0 {
				// 乱序：last 小于上一块 next（pool[2*i+2]）。
				ops = append(ops, op{kind: 3, last: pool[0], next: pool[2*i+3]})
			}
		} else {
			ops = append(ops, op{kind: 1, last: last})
			// Finish 之后的非法操作。
			ops = append(ops, op{kind: 3, last: pool[1], next: pool[2]})
			ops = append(ops, op{kind: 1, last: append(bytes.Clone(last), 0)})
		}
		if rng.Intn(3) == 0 {
			ops = append(ops, op{kind: 2, key: pool[rng.Intn(len(pool))]})
		}
	}
	return ops
}
