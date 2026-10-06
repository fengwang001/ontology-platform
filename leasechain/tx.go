package leasechain

// stateBundle 为可整体提交或回滚的服务状态（除 mutex 外的全部字段）。
type stateBundle struct {
	service   *Service
	cfg       Config
	lastNow   int
	clock     int64
	nextID    int64
	leases    map[int64]*Lease
	arrears   map[int64]*Arrear
	recourses map[int64]*Recourse
	oneTimes  []*OneTimeConsent
	generals  map[string]generalConsent
	bills     map[billKey]*bill
}

func (s *Service) bundleLocked() *stateBundle {
	return &stateBundle{
		cfg:       s.cfg,
		lastNow:   s.lastNow,
		clock:     s.clock,
		nextID:    s.nextID,
		leases:    s.leases,
		arrears:   s.arrears,
		recourses: s.recourses,
		oneTimes:  s.oneTimes,
		generals:  s.generals,
		bills:     s.bills,
	}
}

// cloneLocked 深拷贝全部可变状态，用于事务试执行。
func (b *stateBundle) cloneLocked() *stateBundle {
	cp := &stateBundle{
		cfg:       b.cfg,
		lastNow:   b.lastNow,
		clock:     b.clock,
		nextID:    b.nextID,
		leases:    make(map[int64]*Lease, len(b.leases)),
		arrears:   make(map[int64]*Arrear, len(b.arrears)),
		recourses: make(map[int64]*Recourse, len(b.recourses)),
		generals:  make(map[string]generalConsent, len(b.generals)),
		bills:     make(map[billKey]*bill, len(b.bills)),
	}
	for id, l := range b.leases {
		x := *l
		cp.leases[id] = &x
	}
	for id, a := range b.arrears {
		x := *a
		cp.arrears[id] = &x
	}
	for id, r := range b.recourses {
		x := *r
		cp.recourses[id] = &x
	}
	for _, c := range b.oneTimes {
		x := *c
		cp.oneTimes = append(cp.oneTimes, &x)
	}
	for k, g := range b.generals {
		cp.generals[k] = g
	}
	for k, bv := range b.bills {
		x := *bv
		cp.bills[k] = &x
	}
	return cp
}

func (s *Service) loadBundleLocked(b *stateBundle) {
	s.cfg = b.cfg
	s.lastNow = b.lastNow
	s.clock = b.clock
	s.nextID = b.nextID
	s.leases = b.leases
	s.arrears = b.arrears
	s.recourses = b.recourses
	s.oneTimes = b.oneTimes
	s.generals = b.generals
	s.bills = b.bills
}

func newServiceFromBundle(b *stateBundle) *Service {
	tx := &Service{}
	tx.cfg = b.cfg
	tx.lastNow = b.lastNow
	tx.clock = b.clock
	tx.nextID = b.nextID
	tx.leases = b.leases
	tx.arrears = b.arrears
	tx.recourses = b.recourses
	tx.oneTimes = b.oneTimes
	tx.generals = b.generals
	tx.bills = b.bills
	b.service = tx
	return tx
}

func (b *stateBundle) writeBackFromService(tx *Service) {
	b.cfg = tx.cfg
	b.lastNow = tx.lastNow
	b.clock = tx.clock
	b.nextID = tx.nextID
	b.leases = tx.leases
	b.arrears = tx.arrears
	b.recourses = tx.recourses
	b.oneTimes = tx.oneTimes
	b.generals = tx.generals
	b.bills = tx.bills
}

// txLocked 在深拷贝上执行 fn：fn 返回错误则整体回滚（拒绝不留痕）；
// 返回 nil 则原子提交。fn 通过 tx 访问的 Service 与正式服务方法集相同。
func (s *Service) txLocked(fn func(tx *Service) error) error {
	base := s.bundleLocked()
	work := base.cloneLocked()
	tmp := newServiceFromBundle(work)
	if err := fn(tmp); err != nil {
		return err
	}
	work.writeBackFromService(tmp)
	s.loadBundleLocked(work)
	return nil
}
