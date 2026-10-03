package xfer

// naive 是按题意逐行写出的朴素参考实现，与 Service 走完全独立的数据结构，
// 但严格复刻同一套判定次序、惰性到期落地与崩溃/恢复语义。

type nBucket struct {
	owner   string
	bytes   int64
	uploads int64
}

type nAcct struct {
	limit int64
	used  int64
	count int64
}

type nOffer struct {
	from   string
	to     string
	expiry int64
}

type nLog struct {
	bucket string
	from   string
	to     string
	bytes  int64
	done   int
}

type naive struct {
	maxB       int64
	buckets    map[string]*nBucket
	accts      map[string]*nAcct
	offers     map[string]nOffer
	lastNow    int64
	recovering bool
	log        []nLog
	crashAfter int
}

func newNaive(maxB int) *naive {
	return &naive{maxB: int64(maxB), buckets: map[string]*nBucket{},
		accts: map[string]*nAcct{}, offers: map[string]nOffer{}, crashAfter: -1}
}

func (n *naive) acct(t string) *nAcct {
	a, ok := n.accts[t]
	if !ok {
		a = &nAcct{}
		n.accts[t] = a
	}
	return a
}

func (n *naive) land(bucket string, now int64) {
	if o, ok := n.offers[bucket]; ok && now >= o.expiry {
		delete(n.offers, bucket)
	}
}

func (n *naive) frozen(bucket string, now int64) bool {
	o, ok := n.offers[bucket]
	return ok && now < o.expiry
}

func (n *naive) validNow(now int64) bool { return now >= 0 && now <= maxNow }

func (n *naive) CreateBucket(t, bucket string, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if t == "" || bucket == "" || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	if _, ok := n.buckets[bucket]; ok {
		return ErrExists
	}
	if n.acct(t).count >= n.maxB {
		return ErrTooMany
	}
	n.buckets[bucket] = &nBucket{owner: t}
	n.acct(t).count++
	n.lastNow = now
	return nil
}

func (n *naive) SetLimit(t string, q int64) {
	if n.recovering || t == "" || q < 0 || q > maxQ {
		return
	}
	n.acct(t).limit = q
}

func (n *naive) Put(by, bucket string, size, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if by == "" || bucket == "" || size < 1 || size > maxSize || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if b.owner != by {
		return ErrNotOwner
	}
	n.land(bucket, now)
	if n.frozen(bucket, now) {
		return ErrFrozen
	}
	if n.acct(by).used+size > n.acct(by).limit {
		return ErrQuota
	}
	b.bytes += size
	n.acct(by).used += size
	n.lastNow = now
	return nil
}

func (n *naive) Delete(by, bucket string, size, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if by == "" || bucket == "" || size < 1 || size > maxSize || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if b.owner != by {
		return ErrNotOwner
	}
	if size > b.bytes {
		return ErrInvalid
	}
	n.land(bucket, now)
	if n.frozen(bucket, now) {
		return ErrFrozen
	}
	b.bytes -= size
	n.acct(by).used -= size
	n.lastNow = now
	return nil
}

func (n *naive) BeginUpload(by, bucket string, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if by == "" || bucket == "" || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if b.owner != by {
		return ErrNotOwner
	}
	n.land(bucket, now)
	if n.frozen(bucket, now) {
		return ErrFrozen
	}
	b.uploads++
	n.lastNow = now
	return nil
}

func (n *naive) EndUpload(by, bucket string, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if by == "" || bucket == "" || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if b.uploads == 0 {
		return ErrInvalid
	}
	b.uploads--
	n.land(bucket, now)
	n.lastNow = now
	return nil
}

func (n *naive) Offer(bucket, from, to string, ttl, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if bucket == "" || from == "" || to == "" || ttl < 1 || ttl > maxTTL || !n.validNow(now) {
		return ErrInvalid
	}
	if from == to {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if b.owner != from {
		return ErrNotOwner
	}
	if o, ok := n.offers[bucket]; ok && now < o.expiry {
		return ErrOfferExists
	}
	n.offers[bucket] = nOffer{from: from, to: to, expiry: now + ttl}
	n.lastNow = now
	return nil
}

func (n *naive) Cancel(bucket, by string, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if bucket == "" || by == "" || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	if _, ok := n.buckets[bucket]; !ok {
		return ErrNotFound
	}
	o, ok := n.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= o.expiry {
		return ErrExpired
	}
	if by != o.from && by != o.to {
		return ErrNotParty
	}
	delete(n.offers, bucket)
	n.lastNow = now
	return nil
}

func (n *naive) applyStep(l *nLog) {
	switch l.done {
	case 0:
		n.acct(l.from).used -= l.bytes
		l.done = 1
	case 1:
		n.acct(l.to).used += l.bytes
		l.done = 2
	case 2:
		n.buckets[l.bucket].owner = l.to
		n.acct(l.from).count--
		n.acct(l.to).count++
		delete(n.offers, l.bucket)
		l.done = 3
	case 3:
		l.done = 4
	}
}

func (n *naive) Accept(bucket, by string, now int64) error {
	if n.recovering {
		return ErrNeedRecovery
	}
	if bucket == "" || by == "" || !n.validNow(now) {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockGoBack
	}
	b, ok := n.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	o, ok := n.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= o.expiry {
		return ErrExpired
	}
	if by != o.to {
		return ErrNotParty
	}
	if b.uploads > 0 {
		return ErrUploadsOpen
	}
	if n.acct(o.to).count >= n.maxB {
		return ErrTooMany
	}
	if n.acct(o.to).used+b.bytes > n.acct(o.to).limit {
		return ErrQuota
	}
	n.lastNow = now
	k := n.crashAfter
	n.crashAfter = -1
	l := nLog{bucket: bucket, from: o.from, to: o.to}
	l.bytes = b.bytes
	if k == 0 {
		n.recovering = true
		return ErrNeedRecovery
	}
	n.log = append(n.log, l)
	if k == 1 {
		n.recovering = true
		return ErrNeedRecovery
	}
	rec := &n.log[len(n.log)-1]
	n.applyStep(rec)
	if k == 2 {
		n.recovering = true
		return ErrNeedRecovery
	}
	n.applyStep(rec)
	if k == 3 {
		n.recovering = true
		return ErrNeedRecovery
	}
	n.applyStep(rec)
	if k == 4 {
		n.recovering = true
		return ErrNeedRecovery
	}
	n.applyStep(rec)
	if k == 5 {
		n.recovering = true
		return ErrNeedRecovery
	}
	n.log = n.log[:len(n.log)-1]
	return nil
}

func (n *naive) Recover() {
	if !n.recovering {
		return
	}
	if len(n.log) == 0 {
		n.recovering = false
		return
	}
	rec := &n.log[len(n.log)-1]
	for rec.done < 4 {
		n.applyStep(rec)
	}
	n.log = n.log[:len(n.log)-1]
	n.recovering = false
}

func (n *naive) SetCrashAfter(k int) { n.crashAfter = k }

type nSnapshot struct {
	owners  map[string]string
	bytes   map[string]int64
	uploads map[string]int64
	used    map[string]int64
	limit   map[string]int64
	count   map[string]int64
	offers  map[string]Offer
	lastNow int64
	recover bool
}

func (n *naive) Snapshot() nSnapshot {
	s := nSnapshot{
		owners: map[string]string{}, bytes: map[string]int64{}, uploads: map[string]int64{},
		used: map[string]int64{}, limit: map[string]int64{}, count: map[string]int64{},
		offers: map[string]Offer{}, lastNow: n.lastNow, recover: n.recovering,
	}
	for name, b := range n.buckets {
		s.owners[name] = b.owner
		s.bytes[name] = b.bytes
		s.uploads[name] = b.uploads
	}
	for t, a := range n.accts {
		s.used[t] = a.used
		s.limit[t] = a.limit
		s.count[t] = a.count
	}
	for name, o := range n.offers {
		s.offers[name] = Offer{Bucket: name, From: o.from, To: o.to, Expiry: o.expiry}
	}
	return s
}
