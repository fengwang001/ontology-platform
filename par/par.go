package par

import (
	"sync"

	"ontology/stream"
	"ontology/u8"
)

const probe = 3

type Result struct {
	Output []byte
	Stats  stream.Stats
	Checks int64
	Err    error
}

func Convert(data []byte, cfg stream.Config, k int) Result {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	cfg.FromUTF16 = false
	bounds, probeChecks := starts(data, k)
	parts := make([][]byte, k)
	stats := make([]stream.Stats, k)
	checks := make([]int64, k)
	errs := make([]error, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		start, end := bounds[i], int64(len(data))
		if i+1 < k {
			end = bounds[i+1]
		}
		if start > end {
			start = end
		}
		wg.Add(1)
		go func(i int, start, end int64) {
			defer wg.Done()
			partCfg := cfg
			partCfg.EmitBOM = cfg.EmitBOM && start == 0
			tr := stream.NewAt(partCfg, start)
			n, err := tr.Write(data[start:end])
			if err == nil {
				err = tr.Close()
			} else if int64(n) < end-start {
				tr2 := stream.NewAt(partCfg, start+int64(n))
				_, _ = tr2.Write(data[start+int64(n) : end])
				err = tr2.Close()
			}
			parts[i] = tr.Output()
			stats[i] = tr.Stats()
			checks[i] = tr.Checks()
			errs[i] = err
		}(i, start, end)
	}
	wg.Wait()
	res := Result{Checks: probeChecks}
	for i := 0; i < k; i++ {
		if errs[i] != nil && res.Err == nil {
			res.Err = errs[i]
		}
		res.Output = append(res.Output, parts[i]...)
		addStats(&res.Stats, stats[i])
		res.Checks += checks[i]
	}
	return res
}

func starts(data []byte, k int) ([]int64, int64) {
	bounds := make([]int64, k)
	var probeChecks int64
	for i := 1; i < k; i++ {
		cut := int64(i) * int64(len(data)) / int64(k)
		lo := cut - probe
		if lo < 0 {
			lo = 0
		}
		d := &u8.Decoder{}
		d.Feed(data[lo:cut])
		probeChecks += d.Checks()
		back := int64(d.Pending())
		bounds[i] = cut - back
	}
	return bounds, probeChecks
}

func addStats(dst *stream.Stats, src stream.Stats) {
	dst.Input = src.Input
	dst.Valid += src.Valid
	dst.Invalid += src.Invalid
	dst.InvalidBytes += src.InvalidBytes
	dst.BOMBytes += src.BOMBytes
}
