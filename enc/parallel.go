package enc

import (
	"errors"
	"sync"

	"ontology/wire"
)

// CompressParallel 按固定块并行压缩；块 i 预置上一块末尾 W 字节为字典。
// 每块只依赖「本块原文 + 上一块原文」，故输出与 workers、完成顺序无关，
// 流中块间无刷新标记，仅整流末尾写一次流尾。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: invalid parallel config")
	}
	cfg := Config{WindowCap: DefaultWindow, MaxChain: DefaultChain}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	chunks := make([][]byte, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s, e := i*blockSize, min(i*blockSize+blockSize, len(data))
				bc, _ := newComp(cfg, data[max(0, s-cfg.WindowCap):s], i == 0)
				bc.Write(data[s:e])
				bc.settle(len(bc.pending))
				chunks[i] = bc.Bytes()
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var out []byte
	for _, ch := range chunks {
		out = append(out, ch...)
	}
	out = append(out, wire.TagEnd)
	out = wire.PutVarint(out, uint64(len(data)))
	out = wire.PutVarint(out, wire.FNV(wire.FNVOffset, data))
	return out, nil
}
