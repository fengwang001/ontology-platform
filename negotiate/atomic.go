package negotiate

import "sync/atomic"

func atomicAdd(p *uint64, n uint64) {
	atomic.AddUint64(p, n)
}

func atomicLoad(p *uint64) uint64 {
	return atomic.LoadUint64(p)
}
