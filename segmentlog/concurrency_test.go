package segmentlog

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// lockedWriter 让并发追加/保留时的判定日志也能安全落盘；只保留最近
// maxLogBytes 字节，避免长序列压垮失败输出。
type lockedWriter struct {
	mu          sync.Mutex
	buf         bytes.Buffer
	maxLogBytes int
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.buf.Len() > w.maxLogBytes {
		excess := w.buf.Len() - w.maxLogBytes
		w.buf.Next(excess)
	}
	return n, err
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestConcurrentAppendRetainRead 多执行体并发调用追加、保留与读取，
// 校验：起始位点只进不退、剩余段首尾相接、记录内容与追加时一致。
func TestConcurrentAppendRetainRead(t *testing.T) {
	const (
		writers       = 8
		perWriter     = 300
		retainers     = 2
		readers       = 2
		payloadSize   = 8 // "00-00000"
		maxTotalBytes = 4096
	)
	l := mustNew(t, Config{MaxSegmentBytes: 64, MaxTotalBytes: maxTotalBytes}, t0)
	logs := &lockedWriter{}
	logs.maxLogBytes = 1 << 16
	l.SetDebugOutput(logs)

	var appendWG, workerWG sync.WaitGroup
	errCh := make(chan error, 64)
	report := func(format string, args ...any) {
		select {
		case errCh <- fmt.Errorf(format, args...):
		default:
		}
	}

	// 追加者：固定时间戳 t0（时钟不回退），每条负载可解析出 (writer, seq)。
	for w := 0; w < writers; w++ {
		appendWG.Add(1)
		go func(w int) {
			defer appendWG.Done()
			for seq := 0; seq < perWriter; seq++ {
				payload := []byte(fmt.Sprintf("%02d-%05d", w, seq))
				if _, err := l.Append(Record{Time: t0, Data: payload, Bytes: payloadSize}); err != nil {
					report("append w=%d seq=%d: %v", w, seq, err)
					return
				}
			}
		}(w)
	}

	done := make(chan struct{})

	// 保留者：反复执行大小阶段保留，并校验起始位点单调不减。
	for r := 0; r < retainers; r++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			var lastStart int64
			for {
				select {
				case <-done:
					return
				default:
				}
				rep, err := l.Retain(t0)
				if err != nil {
					report("retain: %v", err)
					return
				}
				if rep.StartOffsetAfter < lastStart {
					report("start offset regressed: %d -> %d", lastStart, rep.StartOffsetAfter)
					return
				}
				lastStart = rep.StartOffsetAfter
			}
		}()
	}

	// 读取者：从当前起始位点读快照，校验记录格式与 per-writer 顺序。
	for r := 0; r < readers; r++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				recs, err := l.Read(l.StartOffset(), 1<<20)
				if err != nil {
					// StartOffset() 与 Read() 之间可能发生保留，起点前移后
					// 旧起点被整体拒绝属正常竞争，取新起点重试即可。
					if errors.Is(err, ErrOutOfRange) {
						continue
					}
					report("read: %v", err)
					return
				}
				seen := map[int]int{}
				for _, rec := range recs {
					w, seq, ok := parsePayload(string(rec))
					if !ok {
						report("bad payload %q", rec)
						return
					}
					if prev, dup := seen[w]; dup && seq <= prev {
						report("out-of-order within snapshot: w=%d seq=%d after %d", w, seq, prev)
						return
					}
					seen[w] = seq
				}
			}
		}()
	}

	appendWG.Wait()
	close(done)
	workerWG.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent error: %v\nlogs tail:\n%s", err, tail(logs.String(), 40))
	}

	// 终态不变量：段首尾相接、总量一致、起始位点与首段对齐。
	segs := l.Segments()
	var sum int64
	for i, seg := range segs {
		sum += seg.Bytes
		if i > 0 && seg.StartOff != segs[i-1].EndOff {
			t.Fatalf("segments not contiguous: %+v", segs)
		}
		if i < len(segs)-1 && seg.Active {
			t.Fatalf("non-final segment active: %+v", segs)
		}
	}
	if sum != l.TotalBytes() {
		t.Fatalf("sum of segment bytes %d != total %d", sum, l.TotalBytes())
	}
	if len(segs) > 0 && segs[0].StartOff != l.StartOffset() {
		t.Fatalf("first segment start %d != log start %d", segs[0].StartOff, l.StartOffset())
	}
	if l.TotalBytes() > maxTotalBytes+64 { // 活动段可超出上限，但至多一个段
		t.Fatalf("total %d exceeds limit + one segment", l.TotalBytes())
	}

	// 最终全量读取：内容可解析且无重复、顺序正确。
	recs, err := l.Read(l.StartOffset(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	uniq := map[string]struct{}{}
	maxSeq := map[int]int{}
	for _, rec := range recs {
		s := string(rec)
		if _, dup := uniq[s]; dup {
			t.Fatalf("duplicate record %q", s)
		}
		uniq[s] = struct{}{}
		w, seq, ok := parsePayload(s)
		if !ok {
			t.Fatalf("bad payload %q", s)
		}
		if seq < maxSeq[w] {
			t.Fatalf("final log out of order: w=%d seq=%d after %d", w, seq, maxSeq[w])
		}
		maxSeq[w] = seq
	}
	if int64(len(recs))*payloadSize != l.TotalBytes() {
		t.Fatalf("read %d records but total=%d", len(recs), l.TotalBytes())
	}
	t.Logf("final: start=%d total=%d segments=%d records=%d", l.StartOffset(), l.TotalBytes(), len(segs), len(recs))
}

func parsePayload(s string) (writer, seq int, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(parts[0])
	q, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return w, q, true
}

func tail(s string, lines int) string {
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}
