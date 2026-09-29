package segmentlog

import "time"

// naiveRec 是朴素参照模型中的一条记录。
type naiveRec struct {
	time  time.Time
	data  []byte
	bytes int64
	start int64
}

// naiveSeg 是与正式实现无关的、直接按题意书写的参照段。
type naiveSeg struct {
	id      int
	records []naiveRec
	bytes   int64
	start   int64
	end     int64
	active  bool
}

func (s *naiveSeg) lastTime() time.Time { return s.records[len(s.records)-1].time }

// naiveLog 用最直白的方式重新实现追加滚动 + 两阶段保留，
// 供随机化测试与正式实现逐字段对比。
type naiveLog struct {
	maxSeg   int64
	maxAge   time.Duration
	maxTotal int64
	segs     []*naiveSeg
	total    int64
	start    int64
	nextID   int
	last     time.Time
}

func newNaive(cfg Config, now time.Time) *naiveLog {
	return &naiveLog{
		maxSeg:   cfg.MaxSegmentBytes,
		maxAge:   cfg.MaxAge,
		maxTotal: cfg.MaxTotalBytes,
		segs:     []*naiveSeg{},
		last:     now,
	}
}

func (n *naiveLog) append(t time.Time, data []byte, b int64) int64 {
	if len(n.segs) == 0 || n.segs[len(n.segs)-1].bytes+b > n.maxSeg {
		if len(n.segs) > 0 {
			n.segs[len(n.segs)-1].active = false
		}
		n.segs = append(n.segs, &naiveSeg{
			id:     n.nextID,
			start:  n.start + n.total,
			end:    n.start + n.total,
			active: true,
		})
		n.nextID++
	}
	seg := n.segs[len(n.segs)-1]
	off := seg.end
	seg.records = append(seg.records, naiveRec{
		time:  t,
		data:  append([]byte(nil), data...),
		bytes: b,
		start: off,
	})
	seg.bytes += b
	seg.end += b
	n.total += b
	if t.After(n.last) {
		n.last = t
	}
	return off
}

func (n *naiveLog) retain(now time.Time) {
	// 时间阶段：从最旧段起，LastTime < now-maxAge 的非活动段逐个删，遇第一个保留即停。
	if n.maxAge > 0 {
		cutoff := now.Add(-n.maxAge)
		drop := 0
		for _, seg := range n.segs {
			if seg.active {
				break
			}
			if !seg.lastTime().Before(cutoff) {
				break
			}
			drop++
		}
		for i := 0; i < drop; i++ {
			n.start += n.segs[i].bytes
			n.total -= n.segs[i].bytes
		}
		n.segs = n.segs[drop:]
	}
	// 大小阶段：总量超限就删最旧非活动段，每删一段重新判定，遇第一个不满足即停。
	if n.maxTotal > 0 {
		for len(n.segs) > 0 && !n.segs[0].active && n.total > n.maxTotal {
			n.start += n.segs[0].bytes
			n.total -= n.segs[0].bytes
			n.segs = n.segs[1:]
		}
	}
	if now.After(n.last) {
		n.last = now
	}
}

func (n *naiveLog) read(start, maxBytes int64) [][]byte {
	out := [][]byte{}
	budget := maxBytes
	on := start == n.start+n.total
	for _, seg := range n.segs {
		for _, rec := range seg.records {
			if !on {
				if rec.start < start {
					continue
				}
				on = true
			}
			if rec.bytes > budget {
				return out
			}
			budget -= rec.bytes
			out = append(out, append([]byte(nil), rec.data...))
		}
	}
	return out
}
