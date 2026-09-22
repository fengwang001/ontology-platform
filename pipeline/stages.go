package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"ontology/merge"
	"ontology/record"
	"ontology/spill"
)

// flushLocked 把当前缓冲排序后溢写为一个 run 文件并落检查点。调用方持有 p.mu。
func (p *Pipeline) flushLocked() error {
	if len(p.buf) == 0 {
		return nil
	}
	sort.Slice(p.buf, func(i, j int) bool { return record.Less(p.buf[i], p.buf[j]) })
	runPath := filepath.Join(p.dir, fmt.Sprintf("run-%06d.run", len(p.runs)))
	if err := spill.WriteRun(runPath, p.buf); err != nil {
		return err
	}
	p.runs = append(p.runs, runPath)
	p.b.Release(p.bufBytes)
	p.buf, p.bufBytes = nil, 0
	return p.saveCheckpointLocked()
}

// mergeLocked 修复各 run 的可恢复前缀后做 K 路归并，写 output.tmp。调用方持有 p.mu。
func (p *Pipeline) mergeLocked() error {
	var sources []merge.Source
	var iters []*spill.Iterator
	defer func() {
		for _, it := range iters {
			it.Close()
		}
	}()
	var total uint64
	for _, run := range p.runs {
		n, err := spill.Repair(run)
		if err != nil {
			return err
		}
		total += n
		it, err := spill.NewIterator(run)
		if err != nil {
			return err
		}
		iters = append(iters, it)
		sources = append(sources, it)
	}
	os.Remove(p.tmpOutput())
	f, err := os.Create(p.tmpOutput())
	if err != nil {
		return err
	}
	w := spill.NewWriter(f, total)
	err = p.merger.Merge(sources, func(rec record.Record) error {
		if err := w.Add(rec); err != nil {
			return err
		}
		return p.callHook(CrashDuringMerge)
	})
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
