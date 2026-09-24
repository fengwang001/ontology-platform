// Package tjoin 实现多 Key 版本表 + 版本水位线 + 事件缓冲的时态连接（依赖 ver，方法可并发调用）。
package tjoin

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/ver"
)

var (
	ErrEmptyKey = errors.New("tjoin: empty key")
	ErrLate     = errors.New("tjoin: late version change (ValidFrom <= vwm)")
	ErrWmBack   = errors.New("tjoin: watermark regression")
	ErrBufFull  = errors.New("tjoin: buffer full")
)

// Joined 是一条连接输出；Seq 为事件到达序号（只有被接受的事件占用）。
type Joined struct {
	Key, Value string
	TS         int64
	Seq        int
	Found      bool
}

type event struct {
	key string
	ts  int64
	seq int
}

// Joiner 是时态连接器；vwm 初始负无穷，Flush 后正无穷。
type Joiner struct {
	mu              sync.Mutex
	maxBuf, nextSeq int
	tables          map[string]*ver.Store
	vwm             int64
	buf             []event
	outputs         []Joined
}

func New(maxBuffered int) *Joiner {
	return &Joiner{maxBuf: maxBuffered, tables: map[string]*ver.Store{}, vwm: math.MinInt64}
}
func (j *Joiner) Upsert(key string, vf int64, value string) error {
	return j.put(key, vf, value, false)
}
func (j *Joiner) Delete(key string, validFrom int64) error { return j.put(key, validFrom, "", true) }

func (j *Joiner) put(key string, validFrom int64, value string, tomb bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	if validFrom <= j.vwm {
		return ErrLate
	}
	if tomb {
		j.table(key).Delete(validFrom)
	} else {
		j.table(key).Upsert(validFrom, value)
	}
	return nil
}
func (j *Joiner) table(key string) *ver.Store {
	t := j.tables[key]
	if t == nil {
		t = &ver.Store{}
		j.tables[key] = t
	}
	return t
}

func (j *Joiner) join(key string, ts int64, seq int) Joined {
	out := Joined{Key: key, TS: ts, Seq: seq}
	if t := j.tables[key]; t != nil {
		out.Value, out.Found = t.AsOf(ts)
	}
	j.outputs = append(j.outputs, out)
	return out
}

func (j *Joiner) Feed(key string, ts int64) ([]Joined, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	if ts <= j.vwm {
		out := j.join(key, ts, j.nextSeq) // 立即输出，不占缓冲
		j.nextSeq++
		return []Joined{out}, nil
	}
	if len(j.buf) >= j.maxBuf {
		return nil, ErrBufFull
	}
	j.buf = append(j.buf, event{key, ts, j.nextSeq})
	j.nextSeq++
	return nil, nil
}

// Watermark 推进水位线并一次性释放 TS <= vwm 的缓冲事件（按 (TS,Seq) 升序）；w == vwm 无操作，w < vwm 拒绝。
func (j *Joiner) Watermark(w int64) ([]Joined, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if w < j.vwm {
		return nil, ErrWmBack
	}
	if w == j.vwm {
		return nil, nil
	}
	j.vwm = w
	return j.release(), nil
}

func (j *Joiner) Flush() []Joined {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.vwm = math.MaxInt64
	return j.release()
}

func (j *Joiner) release() []Joined {
	var ready, keep []event
	for _, e := range j.buf {
		if e.ts <= j.vwm {
			ready = append(ready, e)
		} else {
			keep = append(keep, e)
		}
	}
	j.buf = keep
	sort.Slice(ready, func(a, b int) bool {
		return ready[a].ts < ready[b].ts || ready[a].ts == ready[b].ts && ready[a].seq < ready[b].seq
	})
	out := make([]Joined, 0, len(ready))
	for _, e := range ready {
		out = append(out, j.join(e.key, e.ts, e.seq))
	}
	return out
}

func (j *Joiner) Outputs() []Joined {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Joined(nil), j.outputs...)
}
