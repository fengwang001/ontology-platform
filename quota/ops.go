package quota

// All mutating methods operate on *tree without locking; Ledger holds the
// mutex and owns the swap-in of a cloned tree (Batch rollback).

func quotaErr(op string, d ID, cause error) *QuotaError {
	return &QuotaError{Op: op, Dir: d, Err: cause}
}

// checkChain walks nearest-first, checking bytes before entries at each dir.
func (t *tree) checkChain(op string, chain []ID, addBytes, addEntries int64) error {
	for _, id := range chain {
		n := t.nodes[id]
		if n.bytesQuota >= 0 && n.bytes+n.resAgg+addBytes > n.bytesQuota {
			return quotaErr(op, id, ErrBytesQuota)
		}
		if n.entriesQuota >= 0 && n.entries+addEntries > n.entriesQuota {
			return quotaErr(op, id, ErrEntriesQuota)
		}
	}
	return nil
}

func (t *tree) requireAliveDir(id ID) (*node, error) {
	n, ok := t.get(id)
	if !ok {
		return nil, ErrNotFound
	}
	if !n.isDir {
		return nil, ErrWrongType
	}
	return n, nil
}

func (t *tree) mkdir(p ID) (ID, error) {
	if _, err := t.requireAliveDir(p); err != nil {
		return 0, err
	}
	chain := t.parentChain(p)
	if err := t.checkChain(OpMkdir, chain, 0, 1); err != nil {
		return 0, err
	}
	id := t.allocID()
	d := &node{
		id:           id,
		parent:       p,
		alive:        true,
		isDir:        true,
		bytesQuota:   -1,
		entriesQuota: -1,
	}
	t.nodes = append(t.nodes, d)
	t.children[id] = []ID{}
	t.children[p] = append(t.children[p], id)
	for _, cid := range chain {
		t.nodes[cid].entries++
	}
	return id, nil
}

func (t *tree) addFile(p ID, size int64) (ID, error) {
	if size < 0 {
		return 0, ErrInvalid
	}
	pn, err := t.requireAliveDir(p)
	if err != nil {
		return 0, err
	}
	c := size
	if c > pn.reserve {
		c = pn.reserve
	}
	net := size - c
	chain := t.parentChain(p)
	if err := t.checkChain(OpAddFile, chain, net, 1); err != nil {
		return 0, err
	}
	id := t.allocID()
	f := &node{id: id, parent: p, alive: true, size: size}
	t.nodes = append(t.nodes, f)
	t.children[p] = append(t.children[p], id)
	for _, cid := range chain {
		n := t.nodes[cid]
		n.bytes += size
		n.entries++
		if cid == p {
			n.reserve -= c
		}
		n.resAgg -= c
	}
	return id, nil
}

func (t *tree) setQuota(d ID, b, n int64) error {
	if b < -1 || n < -1 {
		return ErrInvalid
	}
	dn, err := t.requireAliveDir(d)
	if err != nil {
		return err
	}
	if b >= 0 && dn.bytes+dn.resAgg > b {
		return quotaErr(OpSetQuota, d, ErrBelowUsage)
	}
	if n >= 0 && dn.entries > n {
		return quotaErr(OpSetQuota, d, ErrBelowUsage)
	}
	dn.bytesQuota = b
	dn.entriesQuota = n
	return nil
}

func (t *tree) reserve(d ID, b int64) error {
	if b <= 0 {
		return ErrInvalid
	}
	if _, err := t.requireAliveDir(d); err != nil {
		return err
	}
	chain := t.parentChain(d)
	if err := t.checkChain(OpReserve, chain, b, 0); err != nil {
		return err
	}
	for _, cid := range chain {
		n := t.nodes[cid]
		n.resAgg += b
		if cid == d {
			n.reserve += b
		}
	}
	return nil
}

func (t *tree) release(d ID, b int64) error {
	if b <= 0 {
		return ErrInvalid
	}
	dn, err := t.requireAliveDir(d)
	if err != nil {
		return err
	}
	if b > dn.reserve {
		return ErrInsuffReserve
	}
	for _, cid := range t.parentChain(d) {
		n := t.nodes[cid]
		n.resAgg -= b
		if cid == d {
			n.reserve -= b
		}
	}
	return nil
}

func (t *tree) resize(f ID, s int64) error {
	if s < 0 {
		return ErrInvalid
	}
	fn, ok := t.get(f)
	if !ok {
		return ErrNotFound
	}
	if fn.isDir {
		return ErrWrongType
	}
	delta := s - fn.size
	if delta > 0 {
		chain := t.parentChain(fn.parent)
		if err := t.checkChain(OpResize, chain, delta, 0); err != nil {
			return err
		}
		for _, cid := range chain {
			t.nodes[cid].bytes += delta
		}
	} else if delta < 0 {
		for _, cid := range t.parentChain(fn.parent) {
			t.nodes[cid].bytes += delta
		}
	}
	fn.size = s
	return nil
}

func (t *tree) remove(x ID) error {
	if x == 0 {
		return ErrRoot
	}
	xn, ok := t.get(x)
	if !ok {
		return ErrNotFound
	}
	if xn.isDir && len(t.children[x]) > 0 {
		return ErrNotEmpty
	}
	p := xn.parent
	chain := t.parentChain(p)
	var fileBytes, res int64
	if xn.isDir {
		res = xn.reserve
	} else {
		fileBytes = xn.size
	}
	// Refund: bytes (files are never reserved against; deltas were booked as
	// net bytes at AddFile time), one entry, and the removed dir's reserve.
	for _, cid := range chain {
		n := t.nodes[cid]
		n.bytes -= fileBytes
		n.entries--
		n.resAgg -= res
	}
	xn.alive = false
	kids := t.children[p]
	for i, c := range kids {
		if c == x {
			t.children[p] = append(kids[:i], kids[i+1:]...)
			break
		}
	}
	delete(t.children, x)
	return nil
}

func (t *tree) rename(x, p ID) error {
	if x == 0 {
		return ErrRoot
	}
	xn, ok := t.get(x)
	if !ok {
		return ErrNotFound
	}
	if _, err := t.requireAliveDir(p); err != nil {
		return err
	}
	if p == xn.parent {
		return nil
	}
	// p must not be x itself or any strict descendant of x.
	for cur := p; ; {
		if cur == x {
			return ErrBadStructure
		}
		if cur == 0 {
			break
		}
		cur = t.nodes[cur].parent
	}

	var subBytes, subRes, payEntries int64
	if xn.isDir {
		subBytes = xn.bytes
		subRes = xn.resAgg
		payEntries = xn.entries + 1
	} else {
		subBytes = xn.size
		payEntries = 1
	}
	payBytes := subBytes + subRes

	oldSet := map[ID]bool{}
	for _, cid := range t.parentChain(xn.parent) {
		oldSet[cid] = true
	}
	var newOnly []ID
	for _, cid := range t.parentChain(p) {
		if !oldSet[cid] {
			newOnly = append(newOnly, cid)
		}
	}
	if err := t.checkChain(OpRename, newOnly, payBytes, payEntries); err != nil {
		return err
	}

	// Refund the old-only directories.
	newSet := map[ID]bool{}
	for _, cid := range t.parentChain(p) {
		newSet[cid] = true
	}
	for _, cid := range t.parentChain(xn.parent) {
		if !newSet[cid] {
			n := t.nodes[cid]
			n.bytes -= subBytes
			n.resAgg -= subRes
			n.entries -= payEntries
		}
	}
	for _, cid := range newOnly {
		n := t.nodes[cid]
		n.bytes += subBytes
		n.resAgg += subRes
		n.entries += payEntries
	}

	// Rewire.
	oldKids := t.children[xn.parent]
	for i, c := range oldKids {
		if c == x {
			t.children[xn.parent] = append(oldKids[:i], oldKids[i+1:]...)
			break
		}
	}
	t.children[p] = append(t.children[p], x)
	xn.parent = p
	return nil
}

func (t *tree) usage(d ID) (int64, int64, int64, error) {
	n, err := t.requireAliveDir(d)
	if err != nil {
		return 0, 0, 0, err
	}
	return n.bytes, n.entries, n.resAgg, nil
}
