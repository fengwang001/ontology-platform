package chunkcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"testing"
)

// diffOrigin 是一个可在多个版本间切换、可注入失败的随机测试源站。
type diffOrigin struct {
	s        int64
	versions map[string][]byte
	cur      string
	failRate int // 百分之一概率
	rng      *rand.Rand
	mu       struct {
		sync.Mutex
		calls int
	}
}

func (d *diffOrigin) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	d.mu.Lock()
	d.mu.calls++
	if d.failRate > 0 && d.rng.Intn(100) < d.failRate {
		d.mu.Unlock()
		return FetchResult{}, errors.New("diff random failure")
	}
	data := d.versions[d.cur]
	ver := d.cur
	d.mu.Unlock()

	var chunks [][]byte
	for i := req.First; i <= req.Last; i++ {
		lo := int64(i) * d.s
		if lo >= int64(len(data)) {
			chunks = append(chunks, []byte{})
			continue
		}
		hi := lo + d.s
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		chunks = append(chunks, append([]byte(nil), data[lo:hi]...))
	}
	return FetchResult{Version: ver, Length: int64(len(data)), Chunks: chunks}, nil
}

func (d *diffOrigin) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mu.calls
}

type diffLogger struct{ verbose bool }

func (l diffLogger) Logf(format string, args ...any) {
	if l.verbose {
		fmt.Printf("    "+format+"\n", args...)
	}
}

func TestRandomDifferential(t *testing.T) {
	verbose := os.Getenv("CC_VERBOSE") == "1"
	const sequences = 2000
	const opsPerSeq = 24

	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*1000003 + 7)
		rng := rand.New(rand.NewSource(seed))
		s := int64(1 + rng.Intn(5))  // 1..5
		c := 1 + rng.Intn(6)         // 1..6
		nVersions := 1 + rng.Intn(3) // 1..3
		versions := map[string][]byte{}
		var names []string
		maxLen := int(s) * (1 + rng.Intn(6)) // 最多 6 片
		for v := 0; v < nVersions; v++ {
			name := fmt.Sprintf("v%d", v)
			l := rng.Intn(maxLen + 1)
			b := make([]byte, l)
			for i := range b {
				b[i] = byte(rng.Intn(256))
			}
			versions[name] = b
			names = append(names, name)
		}
		keys := []string{"obj", "other"}

		o := &diffOrigin{
			s: s, versions: versions, cur: names[0],
			failRate: 0, rng: rng,
		}
		cache, _ := New(Config{S: s, C: c, Origin: o, Logger: diffLogger{verbose: verbose}})
		naive := newNaiveModel(s, c, o)

		for op := 0; op < opsPerSeq; op++ {
			if rng.Intn(8) == 0 && nVersions > 1 {
				nv := names[rng.Intn(len(names))]
				o.cur = nv
				if verbose {
					fmt.Printf("seq=%d op=%d SWITCH -> %s\n", seq, op, nv)
				}
			}
			key := keys[rng.Intn(len(keys))]
			br, kind := randomRange(rng)
			if verbose {
				fmt.Printf("seq=%d op=%d GET key=%s range=%s\n", seq, op, key, kind)
			}

			before := o.callCount()
			got, gerr := cache.Get(context.Background(), key, br)
			realCalls := o.callCount() - before
			want := naive.get(context.Background(), key, br)
			compareOutcome(t, seed, op, key, kind, got, gerr, want, realCalls)
		}
	}
}

func randomRange(rng *rand.Rand) (ByteRange, string) {
	switch rng.Intn(3) {
	case 0:
		a := int64(rng.Intn(30))
		b := a + int64(rng.Intn(30))
		if rng.Intn(6) == 0 {
			b = a - 1 // 故意 end<start => 构造错误
		}
		r, err := RangeStartEnd(a, b)
		if err != nil {
			return ByteRange{}, fmt.Sprintf("startend(%d,%d)=INVALID", a, b)
		}
		return r, fmt.Sprintf("startend(%d,%d)", a, b)
	case 1:
		a := int64(rng.Intn(30))
		r, _ := RangeFrom(a)
		return r, fmt.Sprintf("from(%d)", a)
	default:
		n := int64(rng.Intn(30))
		r, _ := RangeSuffix(n)
		return r, fmt.Sprintf("suffix(%d)", n)
	}
}

func compareOutcome(t *testing.T, seed int64, op int, key, kind string,
	got *GetResponse, gerr error, want naiveResp, realCalls int) {
	t.Helper()

	if want.err != nil {
		if gerr == nil {
			t.Fatalf("seed=%d op=%d %s %s: want error kind=%d reason=%s, got success data=%x",
				seed, op, key, kind, want.err.Kind, want.err.Reason, got.Data)
		}
		ge, ok := gerr.(*Error)
		if !ok {
			t.Fatalf("seed=%d op=%d: non-chunkcache error %v", seed, op, gerr)
		}
		if ge.Kind != want.err.Kind {
			t.Fatalf("seed=%d op=%d %s %s: error kind=%d want %d (%s)",
				seed, op, key, kind, ge.Kind, want.err.Kind, want.err.Reason)
		}
		return
	}
	if gerr != nil {
		t.Fatalf("seed=%d op=%d %s %s: unexpected error %v (want data len=%d)",
			seed, op, key, kind, gerr, len(want.data))
	}
	if got.Version != want.version {
		t.Fatalf("seed=%d op=%d: version=%q want %q", seed, op, got.Version, want.version)
	}
	if !bytes.Equal(got.Data, want.data) {
		t.Fatalf("seed=%d op=%d %s %s: data=%x want %x",
			seed, op, key, kind, got.Data, want.data)
	}
	// 逐片来源。
	if len(got.Chunks) != len(want.source) {
		t.Fatalf("seed=%d op=%d: chunk reports=%d want %d", seed, op, len(got.Chunks), len(want.source))
	}
	sort.Slice(got.Chunks, func(i, j int) bool { return got.Chunks[i].Index < got.Chunks[j].Index })
	for _, rep := range got.Chunks {
		if rep.Source != want.source[rep.Index] {
			t.Fatalf("seed=%d op=%d: chunk %d source=%v want %v",
				seed, op, rep.Index, rep.Source, want.source[rep.Index])
		}
	}
	// 回源次数（实际源站调用）应与朴素模型一致。
	if realCalls != want.calls {
		t.Fatalf("seed=%d op=%d %s %s: real origin calls=%d want %d",
			seed, op, key, kind, realCalls, want.calls)
	}
}
