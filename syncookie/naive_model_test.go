package syncookie

// refHalf is one entry of the naive half-open queue.
type refHalf struct {
	key     Key
	sisn    uint32
	cisn    uint32
	mss     uint32
	created int64
}

type refAccepted struct {
	key Key
	mss uint32
}

// naive is a straightforward, independently written reference simulator that
// follows the specification line by line.
type naive struct {
	b, a    int
	t       int64
	hash    HashFunc
	nextISN NextISNFunc

	half []refHalf
	acc  []refAccepted

	lastNow  int64
	haveTime bool
	sent     int
	ok       int
	retrans  int
}

func newNaive(B, A int, T int64, h HashFunc, isn NextISNFunc) *naive {
	if B < 1 || B > 1024 || A < 1 || A > 1024 || T < 1 || T > 1_000_000 || h == nil || isn == nil {
		return nil
	}
	return &naive{b: B, a: A, t: T, hash: h, nextISN: isn}
}

func (n *naive) findHalf(key Key) int {
	for i := range n.half {
		if n.half[i].key == key {
			return i
		}
	}
	return -1
}

func (n *naive) prune(now int64) {
	kept := n.half[:0]
	for _, h := range n.half {
		if h.created+n.t <= now {
			continue
		}
		kept = append(kept, h)
	}
	n.half = kept
}

func refMSSIndex(mss uint32) uint32 {
	mi := uint32(0)
	for i := range mssTable {
		if mssTable[i] <= mss {
			mi = uint32(i)
		}
	}
	return mi
}

func (n *naive) OnSyn(now int64, key Key, cisn uint32, mss uint32) (SynReply, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return SynReply{}, ErrInvalidTime
	}
	if n.haveTime && now < n.lastNow {
		return SynReply{}, ErrClockBack
	}
	if mss > 65535 {
		return SynReply{}, ErrInvalidMSS
	}

	n.prune(now)
	n.lastNow, n.haveTime = now, true

	if i := n.findHalf(key); i >= 0 {
		n.retrans++
		return SynReply{ISN: n.half[i].sisn, Cookie: false}, nil
	}
	if len(n.acc) >= n.a {
		return SynReply{}, ErrAcceptFull
	}
	if len(n.half) < n.b {
		h := refHalf{key: key, sisn: n.nextISN(), cisn: cisn, mss: mss, created: now}
		n.half = append(n.half, h)
		return SynReply{ISN: h.sisn, Cookie: false}, nil
	}
	tick := uint32(now / 64000)
	mi := refMSSIndex(mss)
	hv := n.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, tick) & 0xFFFFFF
	isn := (tick%32)<<27 | mi<<24 | hv
	n.sent++
	return SynReply{ISN: isn, Cookie: true}, nil
}

func (n *naive) OnAck(now int64, key Key, seq, ack uint32) (uint32, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidTime
	}
	if n.haveTime && now < n.lastNow {
		return 0, ErrClockBack
	}

	n.prune(now)
	n.lastNow, n.haveTime = now, true

	tick := uint32(now / 64000)
	if i := n.findHalf(key); i >= 0 {
		h := n.half[i]
		if ack != h.sisn+1 || seq != h.cisn+1 {
			return 0, ErrBadAck
		}
		if len(n.acc) >= n.a {
			return 0, ErrAcceptFull
		}
		n.half = append(n.half[:i], n.half[i+1:]...)
		n.acc = append(n.acc, refAccepted{key: key, mss: h.mss})
		return h.mss, nil
	}

	c := ack - 1
	t5 := c >> 27
	mi := (c >> 24) & 7
	hv := c & 0xFFFFFF
	if mi >= 4 {
		return 0, ErrCookie
	}
	age := (tick - t5) & 31
	if age > 1 {
		return 0, ErrCookieExpired
	}
	t0 := tick - age
	if n.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, seq-1, t0)&0xFFFFFF != hv {
		return 0, ErrCookie
	}
	if len(n.acc) >= n.a {
		return 0, ErrAcceptFull
	}
	negotiated := mssTable[mi]
	n.acc = append(n.acc, refAccepted{key: key, mss: negotiated})
	n.ok++
	return negotiated, nil
}

func (n *naive) Accept() (Key, uint32, error) {
	if len(n.acc) == 0 {
		return Key{}, 0, ErrEmpty
	}
	first := n.acc[0]
	n.acc = n.acc[1:]
	return first.key, first.mss, nil
}

func (n *naive) stats() Stats {
	return Stats{
		HalfOpen:   len(n.half),
		Acceptable: len(n.acc),
		CookieSent: n.sent,
		CookieOK:   n.ok,
		Retrans:    n.retrans,
	}
}
