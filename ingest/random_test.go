package ingest_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/dvr"
	"ontology/ingest"
	"ontology/segment"
)

// naiveStream 是保留全部历史片、逐片扫描的朴素模拟，用作对照基准。
type naiveStream struct {
	all          []segment.Segment // 全部已封片（含已淘汰）
	partial      int64
	epoch        int64
	active       bool
	lastActive   int64
	firstOfEpoch bool
	mediaEnd     int64
}

func (st *naiveStream) sealOne(dur int64) {
	seg := segment.Segment{Seq: int64(len(st.all)), Start: st.mediaEnd, Dur: dur}
	if st.firstOfEpoch && seg.Seq != 0 {
		seg.Disc = true
	}
	st.firstOfEpoch = false
	st.mediaEnd += dur
	st.all = append(st.all, seg)
}

func (st *naiveStream) sealPartial() {
	if st.partial > 0 {
		st.sealOne(st.partial)
		st.partial = 0
	}
}

// window 从全部历史片出发整体重算回看窗口。
func (st *naiveStream) window(w int64) []segment.Segment {
	var total int64
	for _, s := range st.all {
		total += s.Dur
	}
	i := 0
	for i < len(st.all) && total-st.all[i].Dur >= w {
		total -= st.all[i].Dur
		i++
	}
	return st.all[i:]
}

type naive struct {
	d, w, t int64
	maxNow  int64
	streams map[string]*naiveStream
}

func newNaive(d, w, t int64) *naive {
	return &naive{d: d, w: w, t: t, maxNow: -1, streams: map[string]*naiveStream{}}
}

func naiveBadNowKey(now int64, key string) bool {
	return now < 0 || now > 1_000_000_000_000 || key == ""
}

func (n *naive) createStream(now int64, key string) error {
	if naiveBadNowKey(now, key) {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClockRegression
	}
	if _, ok := n.streams[key]; ok {
		return ingest.ErrStreamExists
	}
	n.maxNow = now
	n.streams[key] = &naiveStream{firstOfEpoch: true}
	return nil
}

func (n *naive) connect(now int64, key string, force bool) (int64, error) {
	if naiveBadNowKey(now, key) {
		return 0, ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return 0, ingest.ErrClockRegression
	}
	st, ok := n.streams[key]
	if !ok {
		return 0, ingest.ErrStreamNotFound
	}
	if st.active && !force && now-st.lastActive < n.t {
		return 0, ingest.ErrStreamBusy
	}
	n.maxNow = now
	if st.active {
		st.sealPartial()
	}
	st.epoch++
	st.active = true
	st.lastActive = now
	st.firstOfEpoch = true
	return st.epoch, nil
}

func (n *naive) checkConn(st *naiveStream, epoch int64) error {
	if !st.active {
		return ingest.ErrNotConnected
	}
	if epoch < st.epoch {
		return ingest.ErrTakenOver
	}
	if epoch > st.epoch {
		return ingest.ErrEpochInvalid
	}
	return nil
}

func (n *naive) disconnect(now int64, key string, epoch int64) error {
	if naiveBadNowKey(now, key) {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClockRegression
	}
	st, ok := n.streams[key]
	if !ok {
		return ingest.ErrStreamNotFound
	}
	if err := n.checkConn(st, epoch); err != nil {
		return err
	}
	n.maxNow = now
	st.sealPartial()
	st.active = false
	return nil
}

func (n *naive) push(now int64, key string, epoch, dur int64) error {
	if naiveBadNowKey(now, key) || dur < 1 || dur > 60000 {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClockRegression
	}
	st, ok := n.streams[key]
	if !ok {
		return ingest.ErrStreamNotFound
	}
	if err := n.checkConn(st, epoch); err != nil {
		return err
	}
	n.maxNow = now
	st.lastActive = now
	st.partial += dur
	if st.partial >= n.d {
		st.sealOne(st.partial)
		st.partial = 0
	}
	return nil
}

// playlist 逐片扫描全部历史与窗口，独立得出清单与不连续序号。
func (n *naive) playlist(key string, from, to int64) (dvr.List, error) {
	if key == "" || from < 0 || from >= to || to > 1_000_000_000_000_000 {
		return dvr.List{}, ingest.ErrInvalidParam
	}
	st, ok := n.streams[key]
	if !ok {
		return dvr.List{}, ingest.ErrStreamNotFound
	}
	win := st.window(n.w)
	if len(win) == 0 {
		return dvr.List{}, dvr.ErrNotYetProduced
	}
	lo := win[0].Start
	hi := win[len(win)-1].End()
	if from >= hi {
		return dvr.List{}, dvr.ErrNotYetProduced
	}
	if from < lo {
		return dvr.List{}, dvr.ErrSlidOut
	}
	if to > hi {
		to = hi
	}
	var out dvr.List
	for _, s := range win {
		if s.Start < to && from < s.End() {
			out.Segments = append(out.Segments, s)
		}
	}
	out.MediaSequence = out.Segments[0].Seq
	for _, s := range st.all { // 扫描全部历史（含已淘汰）统计标记
		if s.Seq >= out.MediaSequence {
			break
		}
		if s.Disc {
			out.DiscontinuitySequence++
		}
	}
	return out, nil
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) && errors.Is(want, got)
}

func sameList(got, want dvr.List) bool {
	if got.MediaSequence != want.MediaSequence || got.DiscontinuitySequence != want.DiscontinuitySequence {
		return false
	}
	if len(got.Segments) != len(want.Segments) {
		return false
	}
	for i := range got.Segments {
		a, b := got.Segments[i], want.Segments[i]
		if a.Seq != b.Seq || a.Start != b.Start || a.Dur != b.Dur || a.Disc != b.Disc {
			return false
		}
	}
	return true
}

// TestRandomAgainstNaive 1500 组随机操作序列与朴素模拟对照。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	keys := []string{"a", "b", "c"}
	for i := 0; i < sequences; i++ {
		r := rand.New(rand.NewSource(int64(i) + 1443))
		pick := func(small, big int64) int64 { // 偏向小数值以制造边界
			if r.Intn(2) == 0 {
				return 1 + r.Int63n(small)
			}
			return 1 + r.Int63n(big)
		}
		d := pick(8, 60000)
		w := pick(40, 1_000_000_000)
		tt := pick(6, 1_000_000_000)
		svc, err := ingest.NewService(d, w, tt)
		if err != nil {
			t.Fatalf("序列 %d：NewService 失败：%v", i, err)
		}
		nv := newNaive(d, w, tt)
		var log strings.Builder
		fmt.Fprintf(&log, "序列 %d 参数 D=%d W=%d T=%d\n", i, d, w, tt)

		var wall int64
		epochOf := map[string]int64{}
		nextNow := func() int64 {
			now := wall + r.Int63n(4) - 2 // 可能回退或为负（非法）
			if now > wall {
				wall = now
			}
			return now
		}
		pickKey := func() string {
			switch r.Intn(10) {
			case 0:
				return "" // 非法 key
			case 1:
				return "ghost" // 大概率不存在
			default:
				return keys[r.Intn(len(keys))]
			}
		}
		fail := func(format string, args ...any) {
			t.Logf("%s", log.String())
			t.Fatalf(format, args...)
		}

		const ops = 120
		for op := 0; op < ops; op++ {
			switch r.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9: // CreateStream
				now, key := nextNow(), pickKey()
				gotErr := svc.CreateStream(now, key)
				wantErr := nv.createStream(now, key)
				fmt.Fprintf(&log, "op%d CreateStream(%d,%q) -> %v | 期望 %v\n", op, now, key, gotErr, wantErr)
				if !sameErr(gotErr, wantErr) {
					fail("序列 %d op%d CreateStream(%d,%q)：得到 %v，朴素模拟 %v", i, op, now, key, gotErr, wantErr)
				}
			case 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24: // Connect
				now, key := nextNow(), pickKey()
				force := r.Intn(3) == 0
				gotEpoch, gotErr := svc.Connect(now, key, force)
				wantEpoch, wantErr := nv.connect(now, key, force)
				fmt.Fprintf(&log, "op%d Connect(%d,%q,%v) -> (%d,%v) | 期望 (%d,%v)\n",
					op, now, key, force, gotEpoch, gotErr, wantEpoch, wantErr)
				if !sameErr(gotErr, wantErr) || (gotErr == nil && gotEpoch != wantEpoch) {
					fail("序列 %d op%d Connect(%d,%q,%v)：得到 (%d,%v)，朴素模拟 (%d,%v)",
						i, op, now, key, force, gotEpoch, gotErr, wantEpoch, wantErr)
				}
				if gotErr == nil {
					epochOf[key] = gotEpoch
				}
			case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
				40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54,
				55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69: // Push
				now, key := nextNow(), pickKey()
				var dur int64
				switch r.Intn(20) {
				case 0:
					dur = 0 // 非法
				case 1:
					dur = 60001 // 非法
				default:
					dur = pick(10, 60000)
				}
				epoch := epochOf[key]
				switch r.Intn(4) {
				case 0:
					epoch-- // 旧纪元
				case 1:
					epoch++ // 超前纪元
				}
				gotErr := svc.Push(now, key, epoch, dur)
				wantErr := nv.push(now, key, epoch, dur)
				fmt.Fprintf(&log, "op%d Push(%d,%q,%d,%d) -> %v | 期望 %v\n", op, now, key, epoch, dur, gotErr, wantErr)
				if !sameErr(gotErr, wantErr) {
					fail("序列 %d op%d Push(%d,%q,%d,%d)：得到 %v，朴素模拟 %v", i, op, now, key, epoch, dur, gotErr, wantErr)
				}
			case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // Disconnect
				now, key := nextNow(), pickKey()
				epoch := epochOf[key]
				if r.Intn(3) == 0 {
					epoch--
				}
				gotErr := svc.Disconnect(now, key, epoch)
				wantErr := nv.disconnect(now, key, epoch)
				fmt.Fprintf(&log, "op%d Disconnect(%d,%q,%d) -> %v | 期望 %v\n", op, now, key, epoch, gotErr, wantErr)
				if !sameErr(gotErr, wantErr) {
					fail("序列 %d op%d Disconnect(%d,%q,%d)：得到 %v，朴素模拟 %v", i, op, now, key, epoch, gotErr, wantErr)
				}
			default: // Playlist
				key := pickKey()
				var from, to int64
				switch r.Intn(10) {
				case 0:
					from, to = 100, 100 // from>=to 非法
				case 1:
					from, to = -1, 10 // from<0 非法
				case 2:
					from, to = 0, 1_000_000_000_000_001 // to 超界非法
				default:
					from = r.Int63n(2000)
					to = from + 1 + r.Int63n(2000)
				}
				got, gotErr := svc.Playlist(key, from, to)
				want, wantErr := nv.playlist(key, from, to)
				fmt.Fprintf(&log, "op%d Playlist(%q,%d,%d) -> (%v,%v) | 期望 (%v,%v)\n",
					op, key, from, to, got, gotErr, want, wantErr)
				if !sameErr(gotErr, wantErr) {
					fail("序列 %d op%d Playlist(%q,%d,%d)：错误得到 %v，朴素模拟 %v", i, op, key, from, to, gotErr, wantErr)
				}
				if gotErr == nil && !sameList(got, want) {
					fail("序列 %d op%d Playlist(%q,%d,%d)：得到 %+v，朴素模拟（全历史逐片扫描）%+v",
						i, op, key, from, to, got, want)
				}
			}
		}
		t.Logf("%s判定依据：与保留全部历史片、逐片扫描的朴素模拟逐项一致", log.String())
	}
}

// TestReplayDeterminism 相同操作序列重放得到相同的片与清单。
func TestReplayDeterminism(t *testing.T) {
	run := func() map[string]dvr.List {
		r := rand.New(rand.NewSource(99))
		svc, err := ingest.NewService(3000, 50000, 1000)
		if err != nil {
			t.Fatalf("NewService 失败：%v", err)
		}
		var now int64
		epoch := map[string]int64{}
		for i := 0; i < 200; i++ {
			now += 1 + r.Int63n(5)
			key := fmt.Sprintf("k%d", r.Intn(3))
			switch r.Intn(5) {
			case 0:
				_ = svc.CreateStream(now, key)
			case 1:
				if e, err := svc.Connect(now, key, r.Intn(2) == 0); err == nil {
					epoch[key] = e
				}
			case 2:
				_ = svc.Disconnect(now, key, epoch[key])
			default:
				_ = svc.Push(now, key, epoch[key], 1+r.Int63n(6000))
			}
		}
		out := map[string]dvr.List{}
		for _, k := range []string{"k0", "k1", "k2"} {
			l, err := svc.Playlist(k, 0, 1_000_000_000_000_000)
			if err == nil {
				out[k] = l
			}
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("重放结果流数不同：%d vs %d", len(first), len(second))
	}
	for k, want := range first {
		got, ok := second[k]
		if !ok || !sameList(got, want) {
			t.Fatalf("流 %s 重放不一致：\n第一次 %+v\n第二次 %+v", k, want, got)
		}
	}
}

// TestConcurrentStreams 不同流并发操作（配合 -race），事后校验各流不变量：
// seq 连续无洞、相邻片媒体时间首尾相接、第 0 片不带标记。
// 注：本测试用超大 W 不触发淘汰，聚焦并发下的时间线；淘汰语义由
// dvr 单测与 TestRandomAgainstNaive 的小窗口随机对照覆盖。
func TestConcurrentStreams(t *testing.T) {
	const streams = 8
	svc, err := ingest.NewService(3000, 1_000_000_000, 1000)
	if err != nil {
		t.Fatalf("NewService 失败：%v", err)
	}
	var now atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("s%d", id)
			r := rand.New(rand.NewSource(int64(id)))
			step := func() int64 { return now.Add(1 + r.Int63n(3)) }
			// 取 now 与调用之间存在竞态，可能时钟回退，重试直至建流成功。
			created := false
			for tries := 0; tries < 1000 && !created; tries++ {
				err := svc.CreateStream(step(), key)
				created = err == nil
			}
			if !created {
				t.Errorf("%s 建流失败", key)
				return
			}
			epoch := int64(0)
			for j := 0; j < 100; j++ {
				switch r.Intn(4) {
				case 0:
					if e, err := svc.Connect(step(), key, r.Intn(2) == 0); err == nil {
						epoch = e
					}
				case 1:
					_ = svc.Disconnect(step(), key, epoch)
				default:
					_ = svc.Push(step(), key, epoch, 1+r.Int63n(6000))
				}
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < streams; i++ {
		key := fmt.Sprintf("s%d", i)
		got, err := svc.Playlist(key, 0, 1_000_000_000_000_000)
		if errors.Is(err, dvr.ErrNotYetProduced) || errors.Is(err, ingest.ErrStreamNotFound) {
			continue
		}
		if err != nil {
			t.Fatalf("流 %s 清单失败：%v", key, err)
		}
		if got.MediaSequence != 0 || got.DiscontinuitySequence != 0 {
			t.Fatalf("流 %s 全量清单 MS=%d DS=%d，应均为 0", key, got.MediaSequence, got.DiscontinuitySequence)
		}
		for j, sg := range got.Segments {
			if sg.Seq != int64(j) {
				t.Fatalf("流 %s seq 不连续：第 %d 片 seq=%d", key, j, sg.Seq)
			}
			if j > 0 && sg.Start != got.Segments[j-1].End() {
				t.Fatalf("流 %s 时间线断裂：%+v -> %+v", key, got.Segments[j-1], sg)
			}
			if j == 0 && sg.Disc {
				t.Fatalf("流 %s 第 0 片不应带标记", key)
			}
		}
	}
}
