package gengc

// naiveHeap 是独立的朴素参考模型：
//   - 不用写屏障、不维护增量记忆集；
//   - 每次年轻区回收都扫描整个托管空间，从根做全堆可达性；
//   - 每次回收结束时，直接遍历全部年老对象重算记忆集。
//
// 它与 *Heap 实现相同的外部语义，但内部代价与年老区规模相关，
// 用于随机差分对照。状态与判定逻辑在此文件内独立书写，不调用被测代码。
type naiveObject struct {
	id         uint64
	gen        Generation
	fields     []uint64
	youngGCs   int
	promotions int
}

type naiveHeap struct {
	objects   map[uint64]*naiveObject
	tomb      map[uint64]struct{}
	roots     map[uint64]struct{}
	rs        map[uint64]struct{}
	youngCap  int
	oldCap    int
	promoteAt int
	nextID    uint64

	promotions     int
	youngGCs       int
	oldGCs         int
	barrierEntries int
}

func newNaive(cfg Config) *naiveHeap {
	if cfg.PromoteThreshold <= 0 {
		cfg.PromoteThreshold = defaultPromoteThreshold
	}
	if cfg.YoungCapacity < 0 {
		cfg.YoungCapacity = 0
	}
	if cfg.OldCapacity < 0 {
		cfg.OldCapacity = 0
	}
	return &naiveHeap{
		objects:   make(map[uint64]*naiveObject),
		tomb:      make(map[uint64]struct{}),
		roots:     make(map[uint64]struct{}),
		rs:        make(map[uint64]struct{}),
		youngCap:  cfg.YoungCapacity,
		oldCap:    cfg.OldCapacity,
		promoteAt: cfg.PromoteThreshold,
		nextID:    1,
	}
}

func (n *naiveHeap) validate(id uint64, field int, nf int) error {
	if id == 0 || id >= n.nextID {
		return errf(ErrUndefined, "naive", id, field)
	}
	if _, dead := n.tomb[id]; dead {
		return errf(ErrDangling, "naive", id, field)
	}
	if _, ok := n.objects[id]; !ok {
		return errf(ErrDangling, "naive", id, field)
	}
	return nil
}

func (n *naiveHeap) allocate(nf int) (uint64, error) {
	if nf < 0 {
		return 0, errf(ErrInvalidArgument, "naive", 0, nf)
	}
	if n.youngCount() >= n.youngCap {
		saved := n.snapshot()
		n.collectYoung()
		if n.youngCount() >= n.youngCap {
			n.restore(saved)
			return 0, errf(ErrOutOfSpace, "naive", 0, nf)
		}
	}
	id := n.nextID
	n.nextID++
	n.objects[id] = &naiveObject{id: id, fields: make([]uint64, nf)}
	return id, nil
}

func (n *naiveHeap) setField(src uint64, field int, dst uint64) error {
	if err := n.validate(src, field, 0); err != nil {
		return err
	}
	s := n.objects[src]
	if field < 0 || field >= len(s.fields) {
		return errf(ErrInvalidArgument, "naive", src, field)
	}
	if dst != 0 {
		if err := n.validate(dst, field, 0); err != nil {
			return err
		}
	}
	s.fields[field] = dst
	// 朴素模型不使用屏障；但为了与被测实现的“累计登记次数”对照，
	// 按同一口径在每次本应登记的转换上计数（O(1)，仅做观测）。
	if d, ok := n.objects[dst]; ok && s.gen == GenOld && d.gen == GenYoung {
		if _, in := n.rs[src]; !in {
			n.barrierEntries++
		}
	}
	return nil
}

func (n *naiveHeap) getField(src uint64, field int) (uint64, error) {
	if err := n.validate(src, field, 0); err != nil {
		return 0, err
	}
	s := n.objects[src]
	if field < 0 || field >= len(s.fields) {
		return 0, errf(ErrInvalidArgument, "naive", src, field)
	}
	return s.fields[field], nil
}

func (n *naiveHeap) addRoot(id uint64) error {
	if err := n.validate(id, 0, 0); err != nil {
		return err
	}
	n.roots[id] = struct{}{}
	return nil
}

func (n *naiveHeap) removeRoot(id uint64) { delete(n.roots, id) }

func (n *naiveHeap) youngCount() int {
	c := 0
	for _, o := range n.objects {
		if o.gen == GenYoung {
			c++
		}
	}
	return c
}

func (n *naiveHeap) oldCount() int {
	c := 0
	for _, o := range n.objects {
		if o.gen == GenOld {
			c++
		}
	}
	return c
}

// reachable 从根全堆扫描，朴素模型的唯一存活判据。
func (n *naiveHeap) reachable() map[uint64]struct{} {
	live := make(map[uint64]struct{})
	stack := make([]uint64, 0)
	for id := range n.roots {
		if _, ok := n.objects[id]; ok {
			if _, seen := live[id]; !seen {
				live[id] = struct{}{}
				stack = append(stack, id)
			}
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ref := range n.objects[cur].fields {
			if ref == 0 {
				continue
			}
			if _, ok := n.objects[ref]; !ok {
				continue
			}
			if _, seen := live[ref]; !seen {
				live[ref] = struct{}{}
				stack = append(stack, ref)
			}
		}
	}
	return live
}

func (n *naiveHeap) collectYoung() {
	live := n.reachable() // 全堆扫描：朴素做法

	var ids []uint64
	for id := range live {
		if n.objects[id].gen == GenYoung {
			ids = append(ids, id)
		}
	}
	sortIDs(ids)

	promoted := make(map[uint64]struct{})
	for _, id := range ids {
		o := n.objects[id]
		if o.youngGCs < n.promoteAt {
			o.youngGCs++
		}
		if o.youngGCs >= n.promoteAt && n.oldCount() < n.oldCap {
			o.gen = GenOld
			o.youngGCs = n.promoteAt
			o.promotions++
			n.promotions++
			promoted[id] = struct{}{}
		}
		if o.gen == GenYoung && o.youngGCs > n.promoteAt {
			o.youngGCs = n.promoteAt
		}
	}

	dead := make(map[uint64]struct{})
	for id, o := range n.objects {
		if o.gen == GenYoung {
			if _, alive := live[id]; !alive {
				dead[id] = struct{}{}
			}
		}
	}
	for _, o := range n.objects {
		for i, ref := range o.fields {
			if _, bad := dead[ref]; bad {
				o.fields[i] = 0
			}
		}
	}
	for id := range dead {
		n.tomb[id] = struct{}{}
		delete(n.objects, id)
	}

	// 重算记忆集：朴素地遍历“所有”年老对象。
	n.rs = make(map[uint64]struct{})
	var oldIDs []uint64
	for id, o := range n.objects {
		if o.gen == GenOld {
			oldIDs = append(oldIDs, id)
		}
	}
	sortIDs(oldIDs)
	for _, id := range oldIDs {
		o := n.objects[id]
		for _, ref := range o.fields {
			if t, ok := n.objects[ref]; ok && t.gen == GenYoung {
				if _, in := n.rs[id]; !in {
					n.rs[id] = struct{}{}
					// 若此前没有该成员，等价于一次新的屏障登记。
					if _, wasPromoted := promoted[id]; !wasPromoted {
						// 保持与实现一致：晋升产生的成员不计屏障次数。
					}
				}
			}
		}
	}

	n.youngGCs++
}

func (n *naiveHeap) collectOld() {
	live := n.reachable()
	deadOld := make(map[uint64]struct{})
	for id, o := range n.objects {
		if o.gen == GenOld {
			if _, alive := live[id]; !alive {
				deadOld[id] = struct{}{}
			}
		}
	}
	for id := range deadOld {
		delete(n.rs, id)
		n.tomb[id] = struct{}{}
		delete(n.objects, id)
	}
	for _, o := range n.objects {
		for i, ref := range o.fields {
			if _, bad := deadOld[ref]; bad {
				o.fields[i] = 0
			}
		}
	}
	n.oldGCs++
}

type naiveSnap struct {
	objects        map[uint64]*naiveObject
	tomb           map[uint64]struct{}
	roots          map[uint64]struct{}
	rs             map[uint64]struct{}
	nextID         uint64
	promotions     int
	youngGCs       int
	oldGCs         int
	barrierEntries int
}

func (n *naiveHeap) snapshot() naiveSnap {
	objs := make(map[uint64]*naiveObject, len(n.objects))
	for id, o := range n.objects {
		c := *o
		c.fields = append([]uint64(nil), o.fields...)
		objs[id] = &c
	}
	tomb := make(map[uint64]struct{}, len(n.tomb))
	for id := range n.tomb {
		tomb[id] = struct{}{}
	}
	roots := make(map[uint64]struct{}, len(n.roots))
	for id := range n.roots {
		roots[id] = struct{}{}
	}
	rs := make(map[uint64]struct{}, len(n.rs))
	for id := range n.rs {
		rs[id] = struct{}{}
	}
	return naiveSnap{objs, tomb, roots, rs, n.nextID, n.promotions, n.youngGCs, n.oldGCs, n.barrierEntries}
}

func (n *naiveHeap) restore(s naiveSnap) {
	n.objects = s.objects
	n.tomb = s.tomb
	n.roots = s.roots
	n.rs = s.rs
	n.nextID = s.nextID
	n.promotions = s.promotions
	n.youngGCs = s.youngGCs
	n.oldGCs = s.oldGCs
	n.barrierEntries = s.barrierEntries
}

func sortIDs(ids []uint64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
