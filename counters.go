package ontology

import "sync/atomic"

func (idx *Index) CurrentPostingReads() int64 {
	return atomic.LoadInt64(&idx.currentPostingReads)
}
