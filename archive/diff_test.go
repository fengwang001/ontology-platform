package archive_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"ontology/archive"
	"ontology/archive/naive"
)

// randomLogFile 由 RANDOM_TRACE_LOG 指定时，随机对照每步输入/输出/判定写入该文件。
var randomLogFile = os.Getenv("RANDOM_TRACE_LOG")

type opKind int

const (
	opBorrow opKind = iota
	opBatch
	opReserve
	opCancel
	opPickup
	opReturn
	opRenew
	opAdvance
	opDowngrade
	opRestore
	opSeal
)

type step struct {
	kind     opKind
	now      int
	user     int
	vol      int
	approver string
	vols     [3]int
	nvols    int
}

type world struct {
	s      *archive.Service
	m      *naive.Model
	nUsers int
	nVols  int
}

func newWorld(cfg archive.Config, nUsers, nVols int) *world {
	s := archive.NewService(cfg)
	m := naive.New(cfg)
	classes := []archive.Classification{
		archive.ClassPublic, archive.ClassInternal,
		archive.ClassConfidential, archive.ClassTopSecret,
	}
	for i := 0; i < nUsers; i++ {
		// 用户密级：让一部分人只有较低密级，制造密级不足/被跳过场景
		c := classes[i%4]
		if i%4 == 3 {
			c = archive.ClassTopSecret
		}
		s.AddUser(fmt.Sprintf("u%d", i), c)
		m.AddUser(fmt.Sprintf("u%d", i), c)
	}
	for i := 0; i < nVols; i++ {
		c := classes[i%4]
		s.AddVolume(fmt.Sprintf("v%d", i), c)
		m.AddVolume(fmt.Sprintf("v%d", i), c)
	}
	return &world{s: s, m: m, nUsers: nUsers, nVols: nVols}
}

func (w *world) apply(t *testing.T, st step, trace func(string, ...interface{})) {
	uid := fmt.Sprintf("u%d", st.user)
	vid := fmt.Sprintf("v%d", st.vol)
	tag := ""
	var so archive.Outcome
	var mo naive.Result
	var sIdx, mIdx int = -1, -1
	switch st.kind {
	case opBorrow:
		tag = fmt.Sprintf("Borrow now=%d %s %s", st.now, uid, vid)
		so = w.s.Borrow(st.now, uid, vid)
		r := w.m.Borrow(st.now, uid, vid)
		mo, mIdx = r, r.FailIndex
	case opBatch:
		ids := make([]string, st.nvols)
		for i := 0; i < st.nvols; i++ {
			ids[i] = fmt.Sprintf("v%d", st.vols[i])
		}
		tag = fmt.Sprintf("Batch now=%d %s %v", st.now, uid, ids)
		o, i := w.s.BorrowBatch(st.now, uid, ids)
		so, sIdx = o, i
		r := w.m.BorrowBatch(st.now, uid, ids)
		mo, mIdx = r, r.FailIndex
	case opReserve:
		tag = fmt.Sprintf("Reserve now=%d %s %s", st.now, uid, vid)
		so = w.s.Reserve(st.now, uid, vid)
		mo = w.m.Reserve(st.now, uid, vid)
	case opCancel:
		tag = fmt.Sprintf("Cancel now=%d %s %s", st.now, uid, vid)
		so = w.s.CancelReservation(st.now, uid, vid)
		mo = w.m.CancelReservation(st.now, uid, vid)
	case opPickup:
		tag = fmt.Sprintf("Pickup now=%d %s %s", st.now, uid, vid)
		so = w.s.Pickup(st.now, uid, vid)
		mo = w.m.Pickup(st.now, uid, vid)
	case opReturn:
		tag = fmt.Sprintf("Return now=%d %s %s", st.now, uid, vid)
		so = w.s.Return(st.now, uid, vid)
		mo = w.m.Return(st.now, uid, vid)
	case opRenew:
		tag = fmt.Sprintf("Renew now=%d %s %s approver=%q", st.now, uid, vid, st.approver)
		so = w.s.Renew(st.now, uid, vid, st.approver)
		mo = w.m.Renew(st.now, uid, vid, st.approver)
	case opAdvance:
		tag = fmt.Sprintf("Advance now=%d", st.now)
		so = w.s.Advance(st.now)
		mo = w.m.Advance(st.now)
	case opDowngrade:
		tag = fmt.Sprintf("Downgrade %s -> internal", uid)
		so = w.s.SetClassification(uid, archive.ClassInternal)
		mo = w.m.SetClassification(uid, archive.ClassInternal)
	case opRestore:
		tag = fmt.Sprintf("Restore %s -> topsecret", uid)
		so = w.s.SetClassification(uid, archive.ClassTopSecret)
		mo = w.m.SetClassification(uid, archive.ClassTopSecret)
	case opSeal:
		tag = fmt.Sprintf("Seal %s", vid)
		so = w.s.Seal(vid)
		mo = w.m.Seal(vid)
	}
	same := so.OK == mo.OK && so.Err == mo.Err && sIdx == mIdx
	trace("%-48s | svc={ok=%v err=%s idx=%d %q} naive={ok=%v err=%s idx=%d %q} match=%v",
		tag, so.OK, so.Err, sIdx, so.Reason,
		mo.OK, mo.Err, mIdx, mo.Reason, same)
	if !same {
		t.Fatalf("result mismatch at: %s\nsvc ok=%v err=%s idx=%d\nnaive ok=%v err=%s idx=%d\nSVC:\n%s\nNAIVE:\n%s",
			tag, so.OK, so.Err, sIdx, mo.OK, mo.Err, mIdx, w.s.Snapshot(), w.m.Snapshot())
	}
	ss, ms := w.s.Snapshot(), w.m.Snapshot()
	if ss != ms {
		t.Fatalf("state mismatch after: %s\nSVC:\n%s\nNAIVE:\n%s", tag, ss, ms)
	}
}

func randStep(rng *rand.Rand, curNow, nUsers, nVols int) step {
	st := step{now: curNow, user: rng.Intn(nUsers), vol: rng.Intn(nVols)}
	// 让时间以 0..3 的步长推进，偶尔回退以触发时钟错误
	if rng.Intn(8) != 0 {
		st.now = curNow + rng.Intn(4)
	} else {
		st.now = curNow - rng.Intn(3)
	}
	k := opKind(rng.Intn(int(opSeal) + 1))
	st.kind = k
	switch k {
	case opRenew:
		switch rng.Intn(3) {
		case 0:
			st.approver = "boss"
		case 1:
			st.approver = fmt.Sprintf("u%d", st.user) // 自审批
		}
	case opBatch:
		st.nvols = 1 + rng.Intn(3)
		for i := 0; i < st.nvols; i++ {
			st.vols[i] = rng.Intn(nVols)
		}
	}
	return st
}

// TestRandomDifferential：大量随机操作序列对照朴素模型，逐步打印输入、输出与判定。
func TestRandomDifferential(t *testing.T) {
	cfg := archive.Config{
		LoanDays:           [4]int{7, 9, 12, 15},
		PickupDeadlineDays: 2,
		RenewWindowDays:    4,
		MaxRenewals:        1,
		OverdueThreshold:   6,
		CooldownDays:       2,
	}
	nSeeds := 40
	nSteps := 400
	if v := os.Getenv("DIFF_SEEDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			nSeeds = n
		}
	}
	if v := os.Getenv("DIFF_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			nSteps = n
		}
	}
	for seed := int64(1); seed <= int64(nSeeds); seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w := newWorld(cfg, 5, 6)
			cur := 0
			var f *os.File
			trace := t.Logf
			if randomLogFile != "" {
				var err error
				f, err = os.OpenFile(randomLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				trace = func(format string, a ...interface{}) {
					fmt.Fprintf(f, "[seed=%d] %s\n", seed, fmt.Sprintf(format, a...))
				}
			}
			trace("=== seed=%d begin ===", seed)
			for i := 0; i < nSteps; i++ {
				st := randStep(rng, cur, w.nUsers, w.nVols)
				if st.now >= cur {
					cur = st.now
				}
				w.apply(t, st, trace)
			}
			trace("=== seed=%d final snapshot ===\n%s", seed, w.s.Snapshot())
		})
	}
}
