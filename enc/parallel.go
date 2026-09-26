package enc

import (
	"hash/fnv"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

type blockResult struct {
	index int
	data  []byte
}

// compressBlock encodes blocks[bi] with the tail of blocks[bi-1] as preset
// dictionary. The result contains no header and no end record; every block
// except the last ends with a flush marker.
func compressBlock(c config, prev, block []byte, last bool) ([]byte, error) {
	win, err := window.New(c.windowCap)
	if err != nil {
		return nil, err
	}
	mt, err := match.New(win, c.chainLimit)
	if err != nil {
		return nil, err
	}
	dict := prev
	if len(dict) > c.windowCap {
		dict = dict[len(dict)-c.windowCap:]
	}
	mt.Append(dict)
	mt.Commit(0, len(dict), win.Len())
	mt.Append(block)
	var out []byte
	p := block
	i, litStart := 0, 0
	for i < len(p) {
		l, d := mt.Look(p, i)
		if l >= 3 {
			if i > litStart {
				out = wire.AppendLit(out, p[litStart:i])
			}
			out = wire.AppendMatch(out, d, l)
			mt.Commit(len(dict)+litStart, i+l-litStart, win.Len())
			i += l
			litStart = i
			continue
		}
		mt.Commit(len(dict)+i, 1, win.Len())
		i++
	}
	if i > litStart {
		out = wire.AppendLit(out, p[litStart:i])
	}
	if !last {
		out = wire.AppendFlush(out)
	}
	return out, nil
}

// CompressParallel splits data into fixed blocks compressed by up to workers
// goroutines. Each block may reference the last windowCap bytes of the
// previous block; output is deterministic for any worker count.
func CompressParallel(data []byte, blockSize, workers int, opts ...Option) ([]byte, error) {
	if blockSize <= 0 {
		return nil, ErrBlockSize
	}
	if workers <= 0 {
		workers = 1
	}
	c := config{defaultWindowCap, defaultChainLimit}
	for _, o := range opts {
		o(&c)
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	res := make([][]byte, nb)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for bi := 0; bi < nb; bi++ {
		lo := bi * blockSize
		hi := lo + blockSize
		if hi > len(data) {
			hi = len(data)
		}
		plo := lo - c.windowCap
		if plo < 0 {
			plo = 0
		}
		prev, block := data[plo:lo], data[lo:hi]
		wg.Add(1)
		sem <- struct{}{}
		go func(bi int, prev, block []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			b, _ := compressBlock(c, prev, block, bi == nb-1)
			res[bi] = b
		}(bi, prev, block)
	}
	wg.Wait()
	out := wire.AppendHeader(nil, c.windowCap, c.chainLimit)
	sum := fnv.New64a()
	sum.Write(data)
	for _, b := range res {
		out = append(out, b...)
	}
	return wire.AppendEnd(out, len(data), sum.Sum64()), nil
}
