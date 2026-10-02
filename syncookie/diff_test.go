package syncookie

import (
	"fmt"
	"math/rand"
	"testing"
)

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type synRecall struct {
	cisn  uint32
	reply SynReply
}

// genAndPlay builds one random stream dynamically (ACKs reference the actual
// ISN produced by the SYN step) and replays it against both the validator and
// the naive simulator, logging every input, output and decision basis.
func genAndPlay(t *testing.T, tag string, rng *rand.Rand, B, A int, T int64) bool {
	v, errV := New(B, T, A, testHash, newCounterISN())
	n := newNaive(B, A, T, testHash, newCounterISN())
	if (errV == nil) != (n != nil) {
		t.Fatalf("constructor mismatch: %v", errV)
	}
	if errV != nil {
		return true
	}

	now := int64(0)
	const nKeys = 6
	recall := make(map[uint32]synRecall)
	steps := 40 + rng.Intn(60)
	ok := true
	failf := func(format string, args ...any) {
		t.Errorf(format, args...)
		ok = false
	}

	for step := 0; step < steps; step++ {
		// Nondecreasing clock with jumps across ticks, plus rollbacks and
		// out-of-range timestamps.
		switch rng.Intn(12) {
		case 0:
			now -= int64(1 + rng.Intn(10))
		case 1:
			now = int64(rng.Intn(40))*64000 + int64(rng.Intn(64000))
		default:
			now += int64(rng.Intn(8000))
		}
		if now < -5 {
			now = 5
		}
		id := uint32(rng.Intn(nKeys))
		key := mkKey(id)

		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4: // SYN
			cisn := rng.Uint32()
			mssChoices := []uint32{0, 535, 536, 1219, 1220, 1459, 1460, 8959, 8960, 65535}
			mss := mssChoices[rng.Intn(len(mssChoices))]
			useNow, useMSS, forced := now, mss, ""
			switch rng.Intn(14) {
			case 0:
				useNow, forced = -1-int64(rng.Intn(5)), "invalid time"
			case 1:
				useMSS, forced = 65536+uint32(rng.Intn(100)), "invalid mss"
			}
			r1, e1 := v.OnSyn(useNow, key, cisn, useMSS)
			r2, e2 := n.OnSyn(useNow, key, cisn, useMSS)
			s1, s2 := v.Stats(), n.stats()
			t.Logf("[%s #%03d] IN  SYN now=%d key=%d cisn=%#010x mss=%d%s | "+
				"OUT isn=%#010x cookie=%v err=%q stats=%+v | %s",
				tag, step, useNow, id, cisn, useMSS, tagOf(forced),
				r1.ISN, r1.Cookie, errorCode(e1), s1, synBasis(useMSS, r1, e1))
			if r1 != r2 || errorCode(e1) != errorCode(e2) || s1 != s2 {
				failf("SYN MISMATCH impl=%+v %v %+v naive=%+v %v %+v", r1, e1, s1, r2, e2, s2)
			}
			if e1 == nil {
				recall[id] = synRecall{cisn: cisn, reply: r1}
			}
		case 5, 6, 7: // ACK
			var seq, ack uint32
			basis := ""
			if rec, has := recall[id]; has && rng.Intn(2) == 0 {
				seq = rec.cisn + 1
				if rng.Intn(8) == 0 {
					ack = rng.Uint32()
					basis = "half-open: corrupt ack -> ErrBadAck"
				} else {
					ack = rec.reply.ISN + 1
					basis = "half-open: matching ack"
				}
			} else {
				tick := uint32(now / 64000)
				mi := uint32(rng.Intn(5)) // 0..4 (4 is invalid)
				cisn := rng.Uint32()
				stamp := tick
				switch rng.Intn(5) {
				case 1:
					stamp = tick - 1
				case 2:
					stamp = tick - 2
				case 3:
					stamp = tick - uint32(2+rng.Intn(30))
				}
				h := testHash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, stamp) & 0xFFFFFF
				if rng.Intn(6) == 0 {
					h ^= 0x100
					basis = "cookie: corrupt hash"
				} else {
					basis = fmt.Sprintf("cookie: tick=%d stamp5=%d mi=%d", tick, stamp&31, mi)
				}
				c := (stamp%32)<<27 | (mi&7)<<24 | h
				seq, ack = cisn+1, c+1
			}
			m1, e1 := v.OnAck(now, key, seq, ack)
			m2, e2 := n.OnAck(now, key, seq, ack)
			s1, s2 := v.Stats(), n.stats()
			t.Logf("[%s #%03d] IN  ACK now=%d key=%d seq=%#010x ack=%#010x | "+
				"OUT mss=%d err=%q stats=%+v | %s",
				tag, step, now, id, seq, ack, m1, errorCode(e1), s1, basis)
			if m1 != m2 || errorCode(e1) != errorCode(e2) || s1 != s2 {
				failf("ACK MISMATCH impl mss=%d %v %+v | naive mss=%d %v %+v",
					m1, e1, s1, m2, e2, s2)
			}
		case 8: // Accept
			k1, m1, e1 := v.Accept()
			k2, m2, e2 := n.Accept()
			s1, s2 := v.Stats(), n.stats()
			t.Logf("[%s #%03d] IN  ACCEPT | OUT key=(%d,%d) mss=%d err=%q",
				tag, step, k1.CAddr, k1.CPort, m1, errorCode(e1))
			if k1 != k2 || m1 != m2 || errorCode(e1) != errorCode(e2) || s1 != s2 {
				failf("ACCEPT MISMATCH impl %v/%d %v | naive %v/%d %v", k1, m1, e1, k2, m2, e2)
			}
		case 9: // Stats
			s1, s2 := v.Stats(), n.stats()
			t.Logf("[%s #%03d] IN  STATS | OUT %+v", tag, step, s1)
			if s1 != s2 {
				failf("STATS MISMATCH %+v vs %+v", s1, s2)
			}
		}
	}
	return ok
}

func tagOf(forced string) string {
	if forced == "" {
		return ""
	}
	return " [" + forced + "]"
}

func synBasis(mss uint32, r SynReply, err error) string {
	if err != nil {
		return "rejected: " + err.Error()
	}
	if r.Cookie {
		return fmt.Sprintf("half-open full -> cookie mi=%d", mssIndex(mss))
	}
	return "registered or retransmitted in half-open queue"
}

// TestNaiveDifferential replays 2000 random operation sequences against both
// the implementation and the line-by-line naive simulator.
func TestNaiveDifferential(t *testing.T) {
	sequences := 2000
	if raceEnabled {
		sequences = 30 // race builds: keep the same check, fewer replays
	}
	for i := 0; i < sequences; i++ {
		rng := rand.New(rand.NewSource(int64(0xC0FFEE) + int64(i)))
		B := 1 + rng.Intn(4)
		A := 1 + rng.Intn(4)
		T := int64(1 + rng.Intn(20000))
		tag := fmt.Sprintf("seq%04d-B%d-A%d-T%d", i, B, A, T)
		if !genAndPlay(t, tag, rng, B, A, T) {
			return
		}
	}
}
