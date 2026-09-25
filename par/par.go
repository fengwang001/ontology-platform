// Package par normalizes independent byte partitions in parallel.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
	"ontology/ws"
)

type Result struct {
	Output []byte
	Table  *span.Table
}

type frag struct {
	out  []byte
	tab  *span.Table
	info norm.FragmentInfo
}

type endRange struct{ start, end int }

// Normalize splits input at the supplied offsets and processes chunks concurrently.
func Normalize(data []byte, offsets []int, cfg norm.Config) (*Result, error) {
	if len(offsets) > 8 {
		offsets = append([]int(nil), offsets[:8]...)
	}
	bounds := append([]int{0}, offsets...)
	bounds = append(bounds, len(data))
	parts := make([][]byte, 0, len(bounds)-1)
	for i := 1; i < len(bounds); i++ {
		if bounds[i] > bounds[i-1] {
			parts = append(parts, data[bounds[i-1]:bounds[i]])
		}
	}
	fs := make([]frag, len(parts))
	var wg sync.WaitGroup
	for i, p := range parts {
		wg.Add(1)
		go func(i int, p []byte) {
			defer wg.Done()
			n := norm.New(norm.Config{StrictNUL: cfg.StrictNUL, Fragment: true})
			_, _ = n.Write(p)
			_ = n.Close()
			fs[i] = frag{n.Output(), n.Map(), n.FragmentInfo()}
		}(i, p)
	}
	wg.Wait()

	var out []byte
	tab := &span.Table{}
	var carryWS endRange
	carryLen := 0
	origPos, outPos := 0, 0
	for idx, f := range fs {
		fStart, fEnd := bounds[0], 0
		_ = fStart
		fStart = chunkStart(bounds, idx)
		fEnd = chunkEnd(bounds, idx)
		leading := leadingWS(parts[idx])
		if carryLen > 0 {
			decision := byte('\n')
			if leading == len(parts[idx]) && idx == len(fs)-1 {
				decision = 0
			} else if leading < len(parts[idx]) {
				decision = parts[idx][leading]
			}
			if decision == '\n' || decision == '\r' {
				tab.Delete(carryWS.start, carryWS.end, outPos)
			} else if decision != 0 {
				tab.Identity(carryWS.start, carryWS.end, outPos, outPos+carryLen)
				out = append(out, data[carryWS.start:carryWS.end]...)
				outPos += carryLen
			} else {
				tab.Delete(carryWS.start, carryWS.end, outPos)
			}
			carryLen = 0
		}
		if idx > 0 && len(parts[idx]) > 0 && parts[idx][0] == '\n' && lastByte(parts[idx-1]) == '\r' {
			f.tab.DropFirstIdentity()
		} else if idx > 0 && lastByte(parts[idx-1]) == '\r' {
			tab.Identity(fStart-1, fStart, outPos, outPos+1)
			out = append(out, '\n')
			outPos++
		}
		f.tab.Translate(fStart, outPos)
		tab.Append(f.tab)
		out = append(out, f.out...)
		outPos += len(f.out)
		origPos = fEnd
		if f.info.PendingCROffset >= 0 && (idx == len(fs)-1 || firstByte(parts[idx]) != '\n') {
			if idx == len(fs)-1 || firstByte(parts[idx+1]) != '\n' {
				tab.Identity(f.info.PendingCROffset, f.info.PendingCROffset+1, outPos, outPos+1)
				out = append(out, '\n')
				outPos++
			}
		}
		if f.info.PendingWSEnd > f.info.PendingWSStart {
			if carryLen == 0 {
				carryWS = endRange{f.info.PendingWSStart, f.info.PendingWSEnd}
			}
			carryWS.end = f.info.PendingWSEnd
			carryLen = carryWS.end - carryWS.start
			if carryLen > cfg.WSBuffer && cfg.WSBuffer > 0 {
				return nil, &norm.OffsetError{Err: norm.ErrWSLimit, Offset: carryWS.start + cfg.WSBuffer}
			}
		}
	}
	if carryLen > 0 {
		tab.Delete(carryWS.start, carryWS.end, outPos)
	}
	finalize(&out, tab, cfg.Policy, origPos)
	return &Result{out, tab}, nil
}

func leadingWS(p []byte) int {
	n := 0
	for n < len(p) && ws.IsTrailing(p[n]) {
		n++
	}
	return n
}
func firstByte(p []byte) byte {
	if len(p) == 0 {
		return 0
	}
	return p[0]
}
func lastByte(p []byte) byte {
	if len(p) == 0 {
		return 0
	}
	return p[len(p)-1]
}
func chunkStart(bounds []int, idx int) int { return bounds[idx] }
func chunkEnd(bounds []int, idx int) int   { return bounds[idx+1] }

func finalize(out *[]byte, tab *span.Table, policy norm.Policy, origEnd int) {
	if policy == norm.Keep || len(*out) == 0 {
		return
	}
	n := 0
	for n < len(*out) && (*out)[len(*out)-1-n] == '\n' {
		n++
	}
	if n <= 1 {
		return
	}
	keep := len(*out) - n + 1
	from := tab.ToOrig(keep)
	*out = append([]byte(nil), (*out)[:keep]...)
	tab.TrimEnd(keep, from, origEnd)
}
