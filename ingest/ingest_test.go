package ingest_test

import (
	"math/rand"
	"os"
	"strconv"
	"testing"

	"ontology/dvr"
	"ontology/ingest"
)

func mustNew(t *testing.T, d, w, tt int64) *ingest.Service {
	t.Helper()
	svc, err := ingest.New(d, w, tt)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", d, w, tt, err)
	}
	return svc
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: err=%v, want %v", what, got, want)
	}
}

func wantPlaylist(t *testing.T, svc *ingest.Service, key string, from, to int64,
	wantSegs []dvr.Segment, wantMS, wantDS int64) {
	t.Helper()
	pl, err := svc.Playlist([]byte(key), from, to)
	if err != nil {
		t.Fatalf("Playlist(%s,%d,%d): %v", key, from, to, err)
	}
	if pl.MediaSequence != wantMS || pl.DiscontinuitySequence != wantDS {
		t.Fatalf("Playlist(%s,%d,%d): MS=%d DS=%d, want MS=%d DS=%d",
			key, from, to, pl.MediaSequence, pl.DiscontinuitySequence, wantMS, wantDS)
	}
	if len(pl.Segments) != len(wantSegs) {
		t.Fatalf("Playlist(%s,%d,%d): %d segments %+v, want %d",
			key, from, to, len(pl.Segments), pl.Segments, len(wantSegs))
	}
	for i, want := range wantSegs {
		if pl.Segments[i] != want {
			t.Fatalf("Playlist(%s,%d,%d): Segments[%d]=%+v, want %+v",
				key, from, to, i, pl.Segments[i], want)
		}
	}
}

// TestExampleReplay replays the worked example from the specification.
// The example writes wall-clock instants as 1,2,3,7,8,9,10 while T=5000ms,
// so they are read as seconds and scaled to milliseconds here.
func TestExampleReplay(t *testing.T) {
	svc := mustNew(t, 4000, 10000, 5000)
	k := []byte("k")
	wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
	epoch, err := svc.Connect(0, k, false)
	wantErr(t, err, nil, "Connect(0)")
	if epoch != 1 {
		t.Fatalf("epoch=%d, want 1", epoch)
	}
	wantErr(t, svc.Push(1000, k, 1, 2000), nil, "Push(1s)")
	wantErr(t, svc.Push(2000, k, 1, 2000), nil, "Push(2s) seals seq0 [0,4000)")
	wantErr(t, svc.Push(3000, k, 1, 3000), nil, "Push(3s) leaves fragment")
	// 7000-3000=4000 < T: busy.
	_, err = svc.Connect(7000, k, false)
	wantErr(t, err, ingest.ErrStreamBusy, "Connect(7s) not silent yet")
	// 8000-3000=5000 == T: silent takeover, fragment seals as seq1 without mark.
	epoch, err = svc.Connect(8000, k, false)
	wantErr(t, err, nil, "Connect(8s) silent takeover")
	if epoch != 2 {
		t.Fatalf("epoch=%d, want 2", epoch)
	}
	// Old epoch is rejected.
	wantErr(t, svc.Push(9000, k, 1, 1000), ingest.ErrTakenOver, "Push old epoch")
	// Epoch 2 first segment carries the discontinuity mark.
	wantErr(t, svc.Push(9000, k, 2, 5000), nil, "Push(9s) seals seq2")
	wantErr(t, svc.Push(10000, k, 2, 4000), nil, "Push(10s) seals seq3, evicts seq0")

	segs123 := []dvr.Segment{
		{Seq: 1, Start: 4000, Dur: 3000, Disc: false},
		{Seq: 2, Start: 7000, Dur: 5000, Disc: true},
		{Seq: 3, Start: 12000, Dur: 4000, Disc: false},
	}
	wantPlaylist(t, svc, "k", 5000, 13000, segs123, 1, 0)
	wantPlaylist(t, svc, "k", 7000, 8000, segs123[1:2], 2, 0)
	wantPlaylist(t, svc, "k", 12000, 20000, segs123[2:3], 3, 1)
	wantPlaylist(t, svc, "k", 6000, 7000, segs123[0:1], 1, 0)
	_, err = svc.Playlist(k, 3999, 5000)
	wantErr(t, err, dvr.ErrSlidOut, "Playlist(3999,5000) slid out")
	_, err = svc.Playlist(k, 16000, 17000)
	wantErr(t, err, dvr.ErrNotYet, "Playlist(16000,17000) not yet")
}

func TestConnectRules(t *testing.T) {
	t.Run("force立即接管", func(t *testing.T) {
		svc := mustNew(t, 4000, 10000, 5000)
		k := []byte("k")
		wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
		if _, err := svc.Connect(0, k, false); err != nil {
			t.Fatal(err)
		}
		epoch, err := svc.Connect(1, k, true)
		wantErr(t, err, nil, "force Connect")
		if epoch != 2 {
			t.Fatalf("epoch=%d, want 2", epoch)
		}
	})
	t.Run("接管时残片为空不出片", func(t *testing.T) {
		svc := mustNew(t, 4000, 10000, 5000)
		k := []byte("k")
		wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
		if _, err := svc.Connect(0, k, false); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Connect(1, k, true); err != nil {
			t.Fatal(err)
		}
		// Epoch 1 sealed nothing; epoch 2 first segment is still seq 0.
		wantErr(t, svc.Push(2, k, 2, 5000), nil, "Push")
		wantPlaylist(t, svc, "k", 0, 5000,
			[]dvr.Segment{{Seq: 0, Start: 0, Dur: 5000, Disc: false}}, 0, 0)
	})
	t.Run("断开后重连首片带标记", func(t *testing.T) {
		svc := mustNew(t, 4000, 10000, 5000)
		k := []byte("k")
		wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
		if _, err := svc.Connect(0, k, false); err != nil {
			t.Fatal(err)
		}
		wantErr(t, svc.Push(1, k, 1, 3000), nil, "Push leaves fragment")
		wantErr(t, svc.Disconnect(2, k, 1), nil, "Disconnect seals seq0 short")
		epoch, err := svc.Connect(3, k, false)
		wantErr(t, err, nil, "Reconnect")
		if epoch != 2 {
			t.Fatalf("epoch=%d, want 2", epoch)
		}
		wantErr(t, svc.Push(4, k, 2, 4000), nil, "Push seals seq1 with mark")
		wantPlaylist(t, svc, "k", 0, 7000, []dvr.Segment{
			{Seq: 0, Start: 0, Dur: 3000, Disc: false},
			{Seq: 1, Start: 3000, Dur: 4000, Disc: true},
		}, 0, 0)
	})
	t.Run("断开时残片为空不出片", func(t *testing.T) {
		svc := mustNew(t, 4000, 10000, 5000)
		k := []byte("k")
		wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
		if _, err := svc.Connect(0, k, false); err != nil {
			t.Fatal(err)
		}
		wantErr(t, svc.Disconnect(1, k, 1), nil, "Disconnect")
		_, err := svc.Playlist(k, 0, 1)
		wantErr(t, err, dvr.ErrNotYet, "no segment sealed")
	})
}

// TestErrorOrder pins the rejection precedence of every operation.
func TestErrorOrder(t *testing.T) {
	svc := mustNew(t, 4000, 10000, 5000)
	a, b, x := []byte("a"), []byte("b"), []byte("x")
	wantErr(t, svc.CreateStream(0, a), nil, "create a")
	wantErr(t, svc.CreateStream(0, b), nil, "create b")
	if _, err := svc.Connect(0, a, false); err != nil {
		t.Fatal(err)
	}
	wantErr(t, svc.Push(1, a, 1, 1000), nil, "push on a")
	// maxNow is now 1; stream a active at epoch 1, b never connected, x unknown.
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"Push参数非法优先于时钟回退", func() error { return svc.Push(0, x, 1, 0) }, ingest.ErrInvalidParam},
		{"Push参数非法优先于流不存在", func() error { return svc.Push(2, x, 1, 70000) }, ingest.ErrInvalidParam},
		{"Push时钟回退优先于流不存在", func() error { return svc.Push(0, x, 1, 1000) }, ingest.ErrClock},
		{"Push流不存在优先于未连接", func() error { return svc.Push(2, x, 1, 1000) }, ingest.ErrStreamNotFound},
		{"Push未连接优先于纪元检查", func() error { return svc.Push(2, b, 99, 1000) }, ingest.ErrNotConnected},
		{"Push旧纪元已被接管", func() error { return svc.Push(2, a, 0, 1000) }, ingest.ErrTakenOver},
		{"Push未来纪元非法", func() error { return svc.Push(2, a, 2, 1000) }, ingest.ErrEpochInvalid},
		{"Disconnect参数非法优先", func() error { return svc.Disconnect(-1, a, 1) }, ingest.ErrInvalidParam},
		{"Disconnect时钟回退优先于流不存在", func() error { return svc.Disconnect(0, x, 1) }, ingest.ErrClock},
		{"Disconnect流不存在", func() error { return svc.Disconnect(2, x, 1) }, ingest.ErrStreamNotFound},
		{"Disconnect未连接", func() error { return svc.Disconnect(2, b, 1) }, ingest.ErrNotConnected},
		{"Disconnect旧纪元已被接管", func() error { return svc.Disconnect(2, a, 0) }, ingest.ErrTakenOver},
		{"Disconnect未来纪元非法", func() error { return svc.Disconnect(2, a, 2) }, ingest.ErrEpochInvalid},
		{"Connect参数非法优先于时钟回退", func() error { _, e := svc.Connect(-1, nil, false); return e }, ingest.ErrInvalidParam},
		{"Connect时钟回退优先于流不存在", func() error { _, e := svc.Connect(0, x, false); return e }, ingest.ErrClock},
		{"Connect流不存在", func() error { _, e := svc.Connect(2, x, false); return e }, ingest.ErrStreamNotFound},
		{"Connect流占用", func() error { _, e := svc.Connect(2, a, false); return e }, ingest.ErrStreamBusy},
		{"Playlist参数非法优先于流不存在", func() error { _, e := svc.Playlist(x, 5, 5); return e }, ingest.ErrInvalidParam},
		{"Playlist流不存在优先于尚未产生", func() error { _, e := svc.Playlist(x, 0, 5); return e }, ingest.ErrStreamNotFound},
		{"Playlist尚未产生", func() error { _, e := svc.Playlist(b, 0, 5); return e }, dvr.ErrNotYet},
		{"CreateStream重复报流已存在", func() error { return svc.CreateStream(2, a) }, ingest.ErrStreamExists},
		{"CreateStream参数非法优先于已存在", func() error { return svc.CreateStream(-1, a) }, ingest.ErrInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantErr(t, tc.op(), tc.want, tc.name)
		})
	}
	if _, err := ingest.New(0, 1, 1); err != ingest.ErrInvalidParam {
		t.Fatalf("New bad D: %v", err)
	}
	if _, err := ingest.New(1, 0, 1); err != ingest.ErrInvalidParam {
		t.Fatalf("New bad W: %v", err)
	}
	if _, err := ingest.New(1, 1, 1_000_000_001); err != ingest.ErrInvalidParam {
		t.Fatalf("New bad T: %v", err)
	}
}

// TestRejectedOpsKeepState verifies rejected operations change nothing,
// including the global clock and the active publisher's last-active time.
func TestRejectedOpsKeepState(t *testing.T) {
	svc := mustNew(t, 4000, 10000, 5000)
	k := []byte("k")
	wantErr(t, svc.CreateStream(0, k), nil, "CreateStream")
	if _, err := svc.Connect(0, k, false); err != nil {
		t.Fatal(err)
	}
	wantErr(t, svc.Push(1, k, 1, 1000), nil, "Push lastActive=1")
	// Rejected Push with huge now must not latch the clock.
	wantErr(t, svc.Push(1_000_000, k, 1, 0), ingest.ErrInvalidParam, "bad dur")
	// Rejected Push with future epoch must not latch clock nor lastActive.
	wantErr(t, svc.Push(500_000, k, 9, 1000), ingest.ErrEpochInvalid, "bad epoch")
	// Rejected busy Connect (4000-1 < T) must not latch the clock either.
	_, err := svc.Connect(4000, k, false)
	wantErr(t, err, ingest.ErrStreamBusy, "busy")
	// Clock is still at 1: now=2 is accepted.
	wantErr(t, svc.Push(2, k, 1, 1000), nil, "clock not advanced by rejects")
	// lastActive is 2, not 500000: busy at 5001, silent at 5002 (== lastActive+T).
	_, err = svc.Connect(5001, k, false)
	wantErr(t, err, ingest.ErrStreamBusy, "not silent at 5001")
	_, err = svc.Connect(5002, k, false)
	wantErr(t, err, nil, "silent exactly at lastActive+T")
}

// TestDeterministicReplay runs the same operation sequence twice.
func TestDeterministicReplay(t *testing.T) {
	run := func() []dvr.Playlist {
		svc := mustNew(t, 100, 250, 50)
		k := []byte("k")
		wantErr(t, svc.CreateStream(0, k), nil, "create")
		if _, err := svc.Connect(0, k, false); err != nil {
			t.Fatal(err)
		}
		for i := int64(1); i <= 20; i++ {
			wantErr(t, svc.Push(i, k, 1, 30), nil, "push")
		}
		if _, err := svc.Connect(100, k, true); err != nil {
			t.Fatal(err)
		}
		for i := int64(101); i <= 120; i++ {
			wantErr(t, svc.Push(i, k, 2, 45), nil, "push")
		}
		var out []dvr.Playlist
		for from := int64(0); from < 1500; from += 137 {
			pl, err := svc.Playlist(k, from, from+200)
			if err != nil {
				continue
			}
			out = append(out, pl)
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("replay diverged: %d vs %d playlists", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.MediaSequence != b.MediaSequence || a.DiscontinuitySequence != b.DiscontinuitySequence ||
			len(a.Segments) != len(b.Segments) {
			t.Fatalf("replay diverged at query %d: %+v vs %+v", i, a, b)
		}
		for j := range a.Segments {
			if a.Segments[j] != b.Segments[j] {
				t.Fatalf("replay diverged at query %d segment %d", i, j)
			}
		}
	}
}

// naiveStream is the naive reference model: it keeps every sealed segment
// forever and answers queries by scanning the full history one by one.
type naiveStream struct {
	epoch      int64
	active     bool
	lastActive int64
	pending    int64
	epochFirst bool
	all        []dvr.Segment
}

func (st *naiveStream) seal() {
	seq := int64(len(st.all))
	var start int64
	if len(st.all) > 0 {
		start = st.all[len(st.all)-1].End()
	}
	st.all = append(st.all, dvr.Segment{
		Seq: seq, Start: start, Dur: st.pending, Disc: st.epochFirst && seq != 0,
	})
	st.pending = 0
	st.epochFirst = false
}

func (st *naiveStream) flush() {
	if st.pending > 0 {
		st.seal()
	}
}

type naive struct {
	d, w, tt int64
	maxNow   int64
	streams  map[string]*naiveStream
}

func newNaive(d, w, tt int64) *naive {
	return &naive{d: d, w: w, tt: tt, maxNow: -1, streams: map[string]*naiveStream{}}
}

func validNaiveNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (n *naive) createStream(now int64, key string) error {
	if !validNaiveNow(now) || key == "" {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClock
	}
	if _, ok := n.streams[key]; ok {
		return ingest.ErrStreamExists
	}
	n.streams[key] = &naiveStream{}
	n.maxNow = now
	return nil
}

func (n *naive) connect(now int64, key string, force bool) (int64, error) {
	if !validNaiveNow(now) || key == "" {
		return 0, ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return 0, ingest.ErrClock
	}
	st, ok := n.streams[key]
	if !ok {
		return 0, ingest.ErrStreamNotFound
	}
	if st.active {
		if !force && now-st.lastActive < n.tt {
			return 0, ingest.ErrStreamBusy
		}
		st.flush()
	}
	st.epoch++
	st.active = true
	st.lastActive = now
	st.epochFirst = true
	n.maxNow = now
	return st.epoch, nil
}

func (n *naive) disconnect(now int64, key string, epoch int64) error {
	if !validNaiveNow(now) || key == "" {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClock
	}
	st, ok := n.streams[key]
	if !ok {
		return ingest.ErrStreamNotFound
	}
	if !st.active {
		return ingest.ErrNotConnected
	}
	if epoch < st.epoch {
		return ingest.ErrTakenOver
	}
	if epoch > st.epoch {
		return ingest.ErrEpochInvalid
	}
	st.flush()
	st.active = false
	n.maxNow = now
	return nil
}

func (n *naive) push(now int64, key string, epoch, dur int64) error {
	if !validNaiveNow(now) || key == "" || dur < 1 || dur > 60_000 {
		return ingest.ErrInvalidParam
	}
	if now < n.maxNow {
		return ingest.ErrClock
	}
	st, ok := n.streams[key]
	if !ok {
		return ingest.ErrStreamNotFound
	}
	if !st.active {
		return ingest.ErrNotConnected
	}
	if epoch < st.epoch {
		return ingest.ErrTakenOver
	}
	if epoch > st.epoch {
		return ingest.ErrEpochInvalid
	}
	st.lastActive = now
	st.pending += dur
	if st.pending >= n.d {
		st.seal()
	}
	n.maxNow = now
	return nil
}

// windowStart recomputes the eviction result from the full history: the
// window is the suffix all[j:] where dropping one more oldest segment
// would still leave total >= W, i.e. the smallest j with sum(all[j+1:]) < W.
func (n *naive) windowStart(st *naiveStream) int {
	j := len(st.all) - 1
	var suffix int64 // sum of all[j+1:]
	for j > 0 && suffix+st.all[j].Dur < n.w {
		suffix += st.all[j].Dur
		j--
	}
	return j
}

func (n *naive) playlist(key string, from, to int64) (dvr.Playlist, error) {
	if key == "" || from < 0 || from >= to || to > 1_000_000_000_000_000 {
		return dvr.Playlist{}, ingest.ErrInvalidParam
	}
	st, ok := n.streams[key]
	if !ok {
		return dvr.Playlist{}, ingest.ErrStreamNotFound
	}
	if len(st.all) == 0 || from >= st.all[len(st.all)-1].End() {
		return dvr.Playlist{}, dvr.ErrNotYet
	}
	j := n.windowStart(st)
	if from < st.all[j].Start {
		return dvr.Playlist{}, dvr.ErrSlidOut
	}
	hi := st.all[len(st.all)-1].End()
	if to > hi {
		to = hi
	}
	// Linear scan over the whole retained history.
	var segs []dvr.Segment
	for i, sg := range st.all {
		if i < j {
			continue
		}
		if sg.Start < to && sg.End() > from {
			segs = append(segs, sg)
		}
	}
	first := segs[0].Seq
	var ds int64
	for i := int64(0); i < first; i++ {
		if st.all[i].Disc {
			ds++
		}
	}
	return dvr.Playlist{Segments: segs, MediaSequence: first, DiscontinuitySequence: ds}, nil
}

func samePlaylist(a, b dvr.Playlist) bool {
	if a.MediaSequence != b.MediaSequence || a.DiscontinuitySequence != b.DiscontinuitySequence ||
		len(a.Segments) != len(b.Segments) {
		return false
	}
	for i := range a.Segments {
		if a.Segments[i] != b.Segments[i] {
			return false
		}
	}
	return true
}

// TestRandomVsNaive replays 1500 random operation sequences against both
// the service and the naive full-history model, comparing every result.
func TestRandomVsNaive(t *testing.T) {
	seed := int64(1443)
	if s := os.Getenv("SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	r := rand.New(rand.NewSource(seed))
	keys := []string{"a", "b", "c"}
	for seq := 0; seq < 1500; seq++ {
		d := 1 + r.Int63n(60)
		w := 1 + r.Int63n(300)
		tt := 1 + r.Int63n(30)
		svc, err := ingest.New(d, w, tt)
		if err != nil {
			t.Fatal(err)
		}
		nv := newNaive(d, w, tt)
		ops := 10 + r.Intn(40)
		t.Logf("seq=%d D=%d W=%d T=%d ops=%d", seq, d, w, tt, ops)
		now := int64(0)
		for i := 0; i < ops; i++ {
			now += r.Int63n(6)
			if r.Intn(20) == 0 {
				now -= r.Int63n(8) // clock regression, possibly negative
			}
			key := keys[r.Intn(len(keys))]
			switch kind := r.Intn(16); {
			case kind == 0: // CreateStream
				gErr := svc.CreateStream(now, []byte(key))
				nErr := nv.createStream(now, key)
				t.Logf("  op=%d in=CreateStream(%d,%s) out=(%v|%v) 判定=错误一致", i, now, key, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq=%d op=%d CreateStream(%d,%s): svc=%v naive=%v", seq, i, now, key, gErr, nErr)
				}
			case kind <= 3: // Connect
				force := r.Intn(3) == 0
				gEp, gErr := svc.Connect(now, []byte(key), force)
				nEp, nErr := nv.connect(now, key, force)
				t.Logf("  op=%d in=Connect(%d,%s,%v) out=(%d,%v|%d,%v) 判定=纪元与错误一致",
					i, now, key, force, gEp, gErr, nEp, nErr)
				if gErr != nErr || gEp != nEp {
					t.Fatalf("seq=%d op=%d Connect(%d,%s,%v): svc=(%d,%v) naive=(%d,%v)",
						seq, i, now, key, force, gEp, gErr, nEp, nErr)
				}
			case kind <= 9: // Push
				dur := 1 + r.Int63n(80)
				if r.Intn(30) == 0 {
					dur = 0 // invalid
				}
				epoch := int64(0)
				if st, ok := nv.streams[key]; ok {
					epoch = st.epoch
				}
				epoch += r.Int63n(3) - 1
				gErr := svc.Push(now, []byte(key), epoch, dur)
				nErr := nv.push(now, key, epoch, dur)
				t.Logf("  op=%d in=Push(%d,%s,ep=%d,dur=%d) out=(%v|%v) 判定=错误一致",
					i, now, key, epoch, dur, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq=%d op=%d Push(%d,%s,ep=%d,dur=%d): svc=%v naive=%v",
						seq, i, now, key, epoch, dur, gErr, nErr)
				}
			case kind <= 10: // Disconnect
				epoch := int64(0)
				if st, ok := nv.streams[key]; ok {
					epoch = st.epoch
				}
				epoch += r.Int63n(3) - 1
				gErr := svc.Disconnect(now, []byte(key), epoch)
				nErr := nv.disconnect(now, key, epoch)
				t.Logf("  op=%d in=Disconnect(%d,%s,ep=%d) out=(%v|%v) 判定=错误一致",
					i, now, key, epoch, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq=%d op=%d Disconnect(%d,%s,ep=%d): svc=%v naive=%v",
						seq, i, now, key, epoch, gErr, nErr)
				}
			default: // Playlist
				var hi int64
				if st, ok := nv.streams[key]; ok && len(st.all) > 0 {
					hi = st.all[len(st.all)-1].End()
				}
				from := r.Int63n(hi + 40)
				if r.Intn(20) == 0 {
					from = -1 // invalid
				}
				to := from + 1 + r.Int63n(300)
				gPl, gErr := svc.Playlist([]byte(key), from, to)
				nPl, nErr := nv.playlist(key, from, to)
				match := gErr == nErr && (gErr != nil || samePlaylist(gPl, nPl))
				t.Logf("  op=%d in=Playlist(%s,%d,%d) out=(MS=%d,DS=%d,segs=%d,%v|%v) 判定=清单一致:%v",
					i, key, from, to, gPl.MediaSequence, gPl.DiscontinuitySequence,
					len(gPl.Segments), gErr, nErr, match)
				if !match {
					t.Fatalf("seq=%d op=%d Playlist(%s,%d,%d): svc=(%+v,%v) naive=(%+v,%v)",
						seq, i, key, from, to, gPl, gErr, nPl, nErr)
				}
			}
		}
	}
}
