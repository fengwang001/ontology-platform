package syncookie

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naive is an independent, deliberately straightforward reference
// implementation of the specification, written step by step from the rules.
// The randomized test replays identical operation sequences against both
// naive and Validator and requires identical responses and statistics.
type naive struct {
	b       int
	timeout int64
	a       int
	nextISN func() uint32

	half    []halfEntry // unordered; looked up by linear scan
	pending []pendingEntry

	cookieSent uint64
	cookieOK   uint64
	retrans    uint64

	lastNow int64
	hasNow  bool
}

func newNaive(b int, timeout int64, a int, isnStart uint32) *naive {
	return &naive{b: b, timeout: timeout, a: a, nextISN: isnGen(isnStart)}
}

func (n *naive) stats() Stats {
	return Stats{
		HalfOpen:   len(n.half),
		Pending:    len(n.pending),
		CookieSent: n.cookieSent,
		CookieOK:   n.cookieOK,
		Retrans:    n.retrans,
	}
}

func (n *naive) findHalf(key Key) int {
	for i, e := range n.half {
		if e.key == key {
			return i
		}
	}
	return -1
}

// validate mirrors the spec: parameter/clock errors first, and rejected
// operations change nothing (no cleanup, no clock update).
func (n *naive) validate(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidNow
	}
	if n.hasNow && now < n.lastNow {
		return ErrClockBackwards
	}
	return nil
}

// cleanup runs at the head of every accepted operation.
func (n *naive) cleanup(now int64) {
	n.lastNow = now
	n.hasNow = true
	kept := n.half[:0]
	for _, e := range n.half {
		if e.created+n.timeout > now {
			kept = append(kept, e)
		}
	}
	n.half = kept
}

func (n *naive) onSyn(now int64, key Key, cisn uint32, mss int) (SynResult, error, string) {
	if err := n.validate(now); err != nil {
		return SynResult{}, err, "rejected: invalid now or clock regression"
	}
	if mss < 0 || mss > 65535 {
		return SynResult{}, ErrInvalidMSS, "rejected: mss out of range"
	}
	n.cleanup(now)

	if i := n.findHalf(key); i >= 0 {
		n.retrans++
		return SynResult{ISN: n.half[i].sisn, Cookie: false}, nil,
			"key already half-open: retransmit original sisn, created unchanged"
	}
	if len(n.pending) >= n.a {
		return SynResult{}, ErrAcceptFull, "accept queue full: drop"
	}
	if len(n.half) < n.b {
		sisn := n.nextISN()
		n.half = append(n.half, halfEntry{key: key, sisn: sisn, cisn: cisn, mss: uint16(mss), created: now})
		return SynResult{ISN: sisn, Cookie: false}, nil, "half-open slot free: register state"
	}
	t := uint32(now / 64000)
	mi := mssIndex(uint16(mss))
	h := testHash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, t) & 0xFFFFFF
	isn := (t%32)<<27 | mi<<24 | h
	n.cookieSent++
	return SynResult{ISN: isn, Cookie: true}, nil,
		fmt.Sprintf("half-open full: stateless cookie t=%d mi=%d", t, mi)
}

func (n *naive) onAck(now int64, key Key, seq, ack uint32) (error, string) {
	if err := n.validate(now); err != nil {
		return err, "rejected: invalid now or clock regression"
	}
	n.cleanup(now)

	if i := n.findHalf(key); i >= 0 {
		e := n.half[i]
		if ack != e.sisn+1 || seq != e.cisn+1 {
			return ErrBadAck, "half-open entry: ack/seq mismatch, entry kept"
		}
		if len(n.pending) >= n.a {
			return ErrAcceptFull, "half-open entry matched but accept queue full, entry kept"
		}
		n.half = append(n.half[:i], n.half[i+1:]...)
		n.pending = append(n.pending, pendingEntry{key: key, mss: e.mss})
		return nil, "half-open handshake complete: moved to accept queue"
	}

	c := ack - 1
	t5 := c >> 27
	mi := (c >> 24) & 7
	h := c & 0xFFFFFF
	if mi >= 4 {
		return ErrCookie, "cookie mi >= 4"
	}
	t := uint32(now / 64000)
	age := (t%32 - t5 + 32) % 32
	if age > 1 {
		return ErrCookieExpired, fmt.Sprintf("cookie age %d > 1", age)
	}
	t0 := t - age
	if testHash(key.CAddr, key.SAddr, key.CPort, key.SPort, seq-1, t0)&0xFFFFFF != h {
		return ErrCookie, "cookie hash mismatch"
	}
	if len(n.pending) >= n.a {
		return ErrAcceptFull, "cookie valid but accept queue full"
	}
	n.pending = append(n.pending, pendingEntry{key: key, mss: mssTable[mi]})
	n.cookieOK++
	return nil, fmt.Sprintf("cookie verified: t0=%d mi=%d, enqueued", t0, mi)
}

func (n *naive) accept() (Key, uint16, error, string) {
	if len(n.pending) == 0 {
		return Key{}, 0, ErrEmpty, "accept queue empty"
	}
	e := n.pending[0]
	n.pending = n.pending[1:]
	return e.key, e.mss, nil, "dequeued oldest established connection"
}

func errEqual(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// synTrack records what the generator learns from SYN responses so it can
// craft plausible ACKs (both valid and corrupted).
type synTrack struct {
	cisn   uint32
	isn    uint32
	cookie bool
}

// TestRandomSequencesAgainstNaive replays 2000 random operation sequences
// against both implementations and requires identical results.
func TestRandomSequencesAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	keys := []Key{k1(), k2(), k3(), k4(),
		{CAddr: 0x0A000005, CPort: 40005, SAddr: 0x0A0000FE, SPort: 443},
		{CAddr: 0x0A000006, CPort: 40006, SAddr: 0x0A0000FE, SPort: 443},
	}
	mssChoices := []int{0, 500, 536, 1220, 1400, 1459, 1460, 1500, 8960, 65535}
	nowSteps := []int64{0, 0, 1, 999, 5000, 63999, 64000, 70000, 200000}

	for seq := 0; seq < 2000; seq++ {
		b := 1 + rng.Intn(3)
		timeout := int64(500 * (1 + rng.Intn(200))) // 500..100000
		a := 1 + rng.Intn(3)
		isnStart := uint32(rng.Intn(1 << 20))

		v, err := New(b, timeout, a, testHash, isnGen(isnStart))
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		n := newNaive(b, timeout, a, isnStart)

		track := map[Key]synTrack{}
		now := int64(rng.Intn(200000))
		ops := 20 + rng.Intn(10)

		for op := 0; op < ops; op++ {
			now += nowSteps[rng.Intn(len(nowSteps))]
			if now > 1_000_000_000_000 {
				now = 1_000_000_000_000
			}
			choice := rng.Intn(100)
			switch {
			case choice < 45: // SYN
				key := keys[rng.Intn(len(keys))]
				cisn := uint32(rng.Intn(1 << 16))
				mss := mssChoices[rng.Intn(len(mssChoices))]
				gotRes, gotErr := v.OnSyn(now, key, cisn, mss)
				wantRes, wantErr, why := n.onSyn(now, key, cisn, mss)
				t.Logf("seq=%d op=%d OnSyn(now=%d key=%+v cisn=%d mss=%d) -> isn=%d cookie=%v err=%v | %s",
					seq, op, now, key, cisn, mss, gotRes.ISN, gotRes.Cookie, gotErr, why)
				if gotRes != wantRes || !errEqual(gotErr, wantErr) {
					t.Fatalf("seq %d op %d OnSyn: got (%+v, %v), want (%+v, %v)",
						seq, op, gotRes, gotErr, wantRes, wantErr)
				}
				if gotErr == nil {
					track[key] = synTrack{cisn: cisn, isn: gotRes.ISN, cookie: gotRes.Cookie}
				}

			case choice < 80: // ACK, usually crafted from tracked state
				key := keys[rng.Intn(len(keys))]
				var ackSeq, ackAck uint32
				if tr, ok := track[key]; ok && rng.Intn(100) < 80 {
					ackSeq = tr.cisn + 1
					ackAck = tr.isn + 1
					if rng.Intn(100) < 15 { // corrupt one field
						if rng.Intn(2) == 0 {
							ackSeq += 1 + uint32(rng.Intn(3))
						} else {
							ackAck += 1 + uint32(rng.Intn(3))
						}
					}
				} else {
					ackSeq = uint32(rng.Intn(1 << 16))
					ackAck = rng.Uint32()
				}
				gotErr := v.OnAck(now, key, ackSeq, ackAck)
				wantErr, why := n.onAck(now, key, ackSeq, ackAck)
				t.Logf("seq=%d op=%d OnAck(now=%d key=%+v seq=%d ack=%d) -> err=%v | %s",
					seq, op, now, key, ackSeq, ackAck, gotErr, why)
				if !errEqual(gotErr, wantErr) {
					t.Fatalf("seq %d op %d OnAck: got %v, want %v", seq, op, gotErr, wantErr)
				}
				if gotErr == nil {
					delete(track, key)
				}

			case choice < 90: // Accept
				gotKey, gotMSS, gotErr := v.Accept()
				wantKey, wantMSS, wantErr, why := n.accept()
				t.Logf("seq=%d op=%d Accept() -> key=%+v mss=%d err=%v | %s",
					seq, op, gotKey, gotMSS, gotErr, why)
				if gotKey != wantKey || gotMSS != wantMSS || !errEqual(gotErr, wantErr) {
					t.Fatalf("seq %d op %d Accept: got (%+v, %d, %v), want (%+v, %d, %v)",
						seq, op, gotKey, gotMSS, gotErr, wantKey, wantMSS, wantErr)
				}

			case choice < 95: // invalid mss (rejected, no cleanup)
				key := keys[rng.Intn(len(keys))]
				mss := []int{-1, 65536}[rng.Intn(2)]
				gotRes, gotErr := v.OnSyn(now, key, 1, mss)
				wantRes, wantErr, why := n.onSyn(now, key, 1, mss)
				t.Logf("seq=%d op=%d OnSyn(now=%d key=%+v mss=%d) -> err=%v | %s",
					seq, op, now, key, mss, gotErr, why)
				if gotRes != wantRes || !errEqual(gotErr, wantErr) {
					t.Fatalf("seq %d op %d OnSyn(bad mss): got (%+v, %v), want (%+v, %v)",
						seq, op, gotRes, gotErr, wantRes, wantErr)
				}

			default: // invalid now / clock regression (rejected, no cleanup)
				key := keys[rng.Intn(len(keys))]
				badNow := []int64{-1, -100, 1_000_000_000_001, now - 1}[rng.Intn(4)]
				gotRes, gotErr := v.OnSyn(badNow, key, 1, 1400)
				wantRes, wantErr, why := n.onSyn(badNow, key, 1, 1400)
				t.Logf("seq=%d op=%d OnSyn(now=%d key=%+v) -> err=%v | %s",
					seq, op, badNow, key, gotErr, why)
				if gotRes != wantRes || !errEqual(gotErr, wantErr) {
					t.Fatalf("seq %d op %d OnSyn(bad now): got (%+v, %v), want (%+v, %v)",
						seq, op, gotRes, gotErr, wantRes, wantErr)
				}
			}

			if got, want := v.Stats(), n.stats(); got != want {
				t.Fatalf("seq %d op %d stats: got %+v, want %+v", seq, op, got, want)
			}
			if s := v.Stats(); s.HalfOpen > b || s.Pending > a {
				t.Fatalf("seq %d op %d: capacity invariant violated: %+v (B=%d A=%d)",
					seq, op, s, b, a)
			}
		}
	}
}
