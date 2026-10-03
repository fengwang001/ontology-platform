package hotrank

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ---- result comparison ----

func boardEqual(r Result, nr naiveResult) (bool, string) {
	if len(r.Board) != len(nr.board) {
		return false, fmt.Sprintf("board len %d vs %d", len(r.Board), len(nr.board))
	}
	for i := range r.Board {
		a, q := r.Board[i], nr.board[i]
		if a.ID != q.id || a.Score != q.score || a.Rank != q.rank ||
			a.Change != q.change || a.New != q.new {
			return false, fmt.Sprintf("board[%d] %+v vs {id:%s score:%d rank:%d change:%d new:%v}",
				i, a, q.id, q.score, q.rank, q.change, q.new)
		}
	}
	if len(r.Dropped) != len(nr.dropped) {
		return false, fmt.Sprintf("dropped len %d vs %d", len(r.Dropped), len(nr.dropped))
	}
	for i := range r.Dropped {
		if r.Dropped[i] != nr.dropped[i] {
			return false, fmt.Sprintf("dropped[%d] %+v vs %+v", i, r.Dropped[i], nr.dropped[i])
		}
	}
	return true, ""
}

// ---- random op sequences ----

type op struct {
	kind string
	id   string
	d    int64
	t    int64
}

type genConfig struct {
	l, w, m, k, hs int64
	ids            []string
	tmax           int64
	badRate        int // probability (percent) of producing an invalid op
}

func genOp(rng *rand.Rand, cfg genConfig) op {
	id := cfg.ids[rng.Intn(len(cfg.ids))]
	switch rng.Intn(4) {
	case 0, 1, 2:
		d := int64(rng.Intn(200) + 1)
		t := int64(rng.Int63n(cfg.tmax + 1))
		if cfg.badRate > 0 && rng.Intn(100) < cfg.badRate {
			switch rng.Intn(3) {
			case 0:
				id = ""
			case 1:
				d = int64(rng.Intn(2)) * (1_000_000_001) // 0 or >1e9
			case 2:
				t = -1 - rng.Int63n(10)
			}
		}
		return op{kind: "add", id: id, d: d, t: t}
	default:
		t := int64(rng.Int63n(cfg.tmax + 1))
		kind := "snapshot"
		if rng.Intn(3) == 0 {
			kind = "peek"
		}
		return op{kind: kind, t: t}
	}
}

func runAgainstNaive(cfg genConfig, ops []op) ([]string, error) {
	b, err := New(cfg.l, cfg.w, cfg.m, cfg.k, cfg.hs)
	if err != nil {
		return nil, fmt.Errorf("real New: %w", err)
	}
	n := newNaive(cfg.l, cfg.w, cfg.m, cfg.k, cfg.hs)
	log := make([]string, 0, len(ops)+1)
	log = append(log, fmt.Sprintf("CFG L=%d W=%d M=%d K=%d Hs=%d", cfg.l, cfg.w, cfg.m, cfg.k, cfg.hs))

	for _, o := range ops {
		switch o.kind {
		case "add":
			er := b.Add(o.id, o.d, o.t)
			en := n.add(o.id, o.d, o.t)
			log = append(log, fmt.Sprintf("Add(id=%q d=%d t=%d) -> real=%v naive=%v", o.id, o.d, o.t, er, en))
			if !errors.Is(er, en) {
				return log, fmt.Errorf("add error mismatch: real=%v naive=%v", er, en)
			}
			if er == nil {
				// Compare scores for all known ids.
				for _, id := range cfg.ids {
					if sr, sn := b.Score(id), n.score(id); sr != sn {
						return log, fmt.Errorf("score %s real=%d naive=%d", id, sr, sn)
					}
				}
			}
		case "snapshot":
			rr, er := b.Snapshot(o.t)
			nr, en := n.snapshot(o.t)
			log = append(log, fmt.Sprintf("Snapshot(now=%d) -> real err=%v board=%+v dropped=%+v | naive err=%v",
				o.t, er, rr.Board, rr.Dropped, en))
			if !errors.Is(er, en) {
				return log, fmt.Errorf("snapshot error mismatch: real=%v naive=%v", er, en)
			}
			if er == nil {
				if ok, why := boardEqual(rr, nr); !ok {
					return log, fmt.Errorf("snapshot mismatch: %s", why)
				}
			}
		case "peek":
			rr, er := b.Peek(o.t)
			nr, en := n.peek(o.t)
			log = append(log, fmt.Sprintf("Peek(now=%d) -> real err=%v board=%+v dropped=%+v | naive err=%v",
				o.t, er, rr.Board, rr.Dropped, en))
			if !errors.Is(er, en) {
				return log, fmt.Errorf("peek error mismatch: real=%v naive=%v", er, en)
			}
			if er == nil {
				if ok, why := boardEqual(rr, nr); !ok {
					return log, fmt.Errorf("peek mismatch: %s", why)
				}
			}
		}
	}
	return log, nil
}

// replayResult records the observable output of a sequence for determinism.
func replayFingerprint(cfg genConfig, ops []op) string {
	b, _ := New(cfg.l, cfg.w, cfg.m, cfg.k, cfg.hs)
	var sb strings.Builder
	for _, o := range ops {
		switch o.kind {
		case "add":
			fmt.Fprintf(&sb, "A%v|", b.Add(o.id, o.d, o.t))
		case "snapshot":
			r, e := b.Snapshot(o.t)
			fmt.Fprintf(&sb, "S%v%v|", e, r)
		case "peek":
			r, e := b.Peek(o.t)
			fmt.Fprintf(&sb, "P%v%v|", e, r)
		}
	}
	return sb.String()
}

func TestRandomDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	var full strings.Builder
	failCount := 0
	for g := 0; g < 2000; g++ {
		cfg := genConfig{
			l:    int64(1 + rng.Intn(12)),
			w:    int64(1 + rng.Intn(6)),
			m:    int64(1 + rng.Intn(300)),
			k:    int64(1 + rng.Intn(5)),
			hs:   int64(1 + rng.Intn(4)),
			tmax: int64(60 + rng.Intn(200)),
		}
		nid := 1 + rng.Intn(7)
		cfg.ids = make([]string, nid)
		for i := range cfg.ids {
			cfg.ids[i] = "id" + strconv.Itoa(i)
		}
		// Every ~100th group includes illegal ops to exercise rejections.
		if g%97 == 3 {
			cfg.badRate = 15
		}
		ops := make([]op, 30+rng.Intn(70))
		for i := range ops {
			ops[i] = genOp(rng, cfg)
		}

		logLines, err := runAgainstNaive(cfg, ops)
		for _, line := range logLines {
			fmt.Fprintln(&full, line)
		}
		if err != nil {
			failCount++
			t.Errorf("group %d: %v", g, err)
			for _, line := range logLines {
				t.Log(line)
			}
			if failCount >= 3 {
				break
			}
			continue
		}
		// Determinism: replay must give the identical fingerprint.
		fp1 := replayFingerprint(cfg, ops)
		fp2 := replayFingerprint(cfg, ops)
		if fp1 != fp2 {
			t.Fatalf("group %d: nondeterministic replay", g)
		}
		fmt.Fprintf(&full, "GROUP %d VERDICT OK\n\n", g)
	}

	path := "/tmp/hotrank-diff.log"
	if err := os.WriteFile(path, []byte(full.String()), 0o644); err != nil {
		t.Logf("write log: %v", err)
	} else {
		t.Logf("input/output/verdict log written to %s", path)
	}
}
