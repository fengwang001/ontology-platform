package par

import (
	"sync"

	"ontology/stream"
	"ontology/u8"
)

type Result struct {
	Output []byte
	Stats stream.Stats
	Err error
}

func Transcode(input []byte, cfg stream.Config, k int) Result {
	if cfg.From != stream.UTF8 {
		return Result{Err: stream.ErrClosed}
	}
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	cuts := make([]int, k+1)
	for j := 0; j < k; j++ {
		cuts[j] = j * len(input) / k
	}
	cuts[k] = len(input)
	out := make([][]byte, k)
	stats := make([]stream.Stats, k)
	errs := make([]error, k)
	var wg sync.WaitGroup
	for j := 0; j < k; j++ {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			start, end := cuts[j], len(input)
			if j > 0 {
				start = ownerStart(input, start)
			}
			if j+1 < k {
				end = ownerStart(input, cuts[j+1])
			}
			cc := cfg
			cc.Limit = 0
			cc.NoBOM = start != 0
			tr := stream.NewAt(cc, int64(start))
			_, _ = tr.Write(input[start:end])
			errs[j] = tr.Close()
			out[j] = append([]byte(nil), tr.Output()...)
			stats[j] = tr.Stats()
		}()
	}
	wg.Wait()
	var total stream.Stats
	n := 0
	for _, b := range out {
		n += len(b)
	}
	all := make([]byte, 0, n)
	for j := range out {
		all = append(all, out[j]...)
		addStats(&total, stats[j])
	}
	return Result{Output: all, Stats: total, Err: firstError(errs)}
}

func ownerStart(p []byte, cut int) int {
	for d := 0; d <= 3 && cut-1-d >= 0; d++ {
		q := cut - 1 - d
		if u8.PendingLead(p[q]) && q+u8.Need(p[q]) > cut {
			return q
		}
	}
	return cut
}

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func addStats(dst *stream.Stats, src stream.Stats) {
	dst.Scalars += src.Scalars
	dst.Invalid += src.Invalid
	dst.InvalidBytes += src.InvalidBytes
	dst.BOMBytes += src.BOMBytes
	dst.Consumed += src.Consumed
	dst.Checks += src.Checks
	dst.AlignChecks += src.AlignChecks
	dst.Output += src.Output
}
