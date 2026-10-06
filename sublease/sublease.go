package sublease

import "fmt"

// CreateMasterLease signs a master lease between a landlord and a
// tenant. start is the first day in lease, end the first day out.
func (s *Service) CreateMasterLease(now int, landlord, tenant string, start, end int, rent int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if landlord == "" || tenant == "" {
		return "", newErr(ErrInvalidArgument, "landlord and tenant must be non-empty")
	}
	if landlord == tenant {
		return "", newErr(ErrInvalidArgument, "landlord %q cannot lease to itself", landlord)
	}
	if start < 0 || end <= start {
		return "", newErr(ErrInvalidArgument, "invalid term [%d, %d)", start, end)
	}
	if rent <= 0 {
		return "", newErr(ErrInvalidArgument, "rent must be positive, got %d", rent)
	}
	ns, err := s.begin(now)
	if err != nil {
		return "", err
	}
	ns.leaseSeq++
	id := fmt.Sprintf("L%d", ns.leaseSeq)
	ns.leases[id] = &Lease{
		ID: id, Receiver: landlord, Tenant: tenant,
		Start: start, End: end, Rent: rent, Depth: 1, Status: Active,
	}
	s.commit(ns)
	return id, nil
}

// CreateSublease lets the tenant of parentLeaseID sublet the whole
// premises to newTenant. Checks run in the fixed error order and only
// the first failure is reported; a rejected call changes nothing.
func (s *Service) CreateSublease(now int, parentLeaseID, newTenant string, start, end int, rent int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 1. 参数非法
	if parentLeaseID == "" || newTenant == "" {
		return "", newErr(ErrInvalidArgument, "parent lease and new tenant must be non-empty")
	}
	if start < 0 || end <= start {
		return "", newErr(ErrInvalidArgument, "invalid term [%d, %d)", start, end)
	}
	if rent <= 0 {
		return "", newErr(ErrInvalidArgument, "rent must be positive, got %d", rent)
	}
	// 2. 时钟回退
	ns, err := s.begin(now)
	if err != nil {
		return "", err
	}
	// 3. 租约不存在或已终止
	p, ok := ns.leases[parentLeaseID]
	if !ok || p.Status != Active {
		return "", newErr(ErrNotFound, "parent lease %q not found or not active", parentLeaseID)
	}
	// 4. 未取得同意
	landlord := ns.topLandlord(p)
	c := ns.consents[landlord][p.Tenant]
	useOneTime := false
	switch {
	case c != nil && c.blanket:
	case c != nil && c.oneTime > 0:
		useOneTime = true
	default:
		return "", newErr(ErrNoConsent, "landlord %q has not consented to subletting by %q", landlord, p.Tenant)
	}
	// 5. 期限越出上级
	if start < p.Start || end > p.End {
		return "", newErr(ErrTermOutOfParent, "term [%d, %d) not within parent [%d, %d)", start, end, p.Start, p.End)
	}
	// 6. 租金超过倍数上限
	if rent*100 > p.Rent*int64(s.cfg.RentCapPercent) {
		return "", newErr(ErrRentCapExceeded, "rent %d exceeds %d%% of parent rent %d", rent, s.cfg.RentCapPercent, p.Rent)
	}
	// 7. 链深度超限
	if p.Depth+1 > s.cfg.MaxDepth {
		return "", newErr(ErrDepthExceeded, "chain depth %d would exceed max %d", p.Depth+1, s.cfg.MaxDepth)
	}
	// 8. 状态不允许该操作
	if p.Child != "" {
		return "", newErr(ErrStateNotAllowed, "lease %q already has effective sublease %q", p.ID, p.Child)
	}
	if newTenant == p.Tenant {
		return "", newErr(ErrStateNotAllowed, "cannot sublease to oneself %q", newTenant)
	}
	ns.leaseSeq++
	id := fmt.Sprintf("L%d", ns.leaseSeq)
	ns.leases[id] = &Lease{
		ID: id, Receiver: p.Tenant, Tenant: newTenant, Parent: p.ID,
		Start: start, End: end, Rent: rent, Depth: p.Depth + 1, Status: Active,
	}
	p.Child = id
	if useOneTime {
		c.oneTime--
	}
	s.commit(ns)
	return id, nil
}

// GrantConsent lets a landlord consent to future subletting by tenant.
// A one-time consent is consumed by a single sublease creation; a
// blanket consent covers all later subleases by that tenant until
// revoked.
func (s *Service) GrantConsent(now int, landlord, tenant string, blanket bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if landlord == "" || tenant == "" {
		return newErr(ErrInvalidArgument, "landlord and tenant must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	m := ns.consents[landlord]
	if m == nil {
		m = make(map[string]*consent)
		ns.consents[landlord] = m
	}
	c := m[tenant]
	if c == nil {
		c = &consent{}
		m[tenant] = c
	}
	if blanket {
		c.blanket = true
	} else {
		c.oneTime++
	}
	s.commit(ns)
	return nil
}

// RevokeBlanketConsent withdraws the landlord's blanket consent for a
// tenant. Subleases already established are unaffected; only subleases
// initiated after the revocation need a new consent.
func (s *Service) RevokeBlanketConsent(now int, landlord, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if landlord == "" || tenant == "" {
		return newErr(ErrInvalidArgument, "landlord and tenant must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	c := ns.consents[landlord][tenant]
	if c == nil || !c.blanket {
		return newErr(ErrStateNotAllowed, "no active blanket consent from %q to %q", landlord, tenant)
	}
	c.blanket = false
	s.commit(ns)
	return nil
}

// GrantRecognition lets the landlord independently recognize a
// currently effective lease, so it survives the termination of its
// ancestors and is promoted to a direct lease with the landlord.
func (s *Service) GrantRecognition(now int, landlord, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if landlord == "" || leaseID == "" {
		return newErr(ErrInvalidArgument, "landlord and lease must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	l, ok := ns.leases[leaseID]
	if !ok || l.Status != Active {
		return newErr(ErrNotFound, "lease %q not found or not active", leaseID)
	}
	if ns.topLandlord(l) != landlord {
		return newErr(ErrInvalidArgument, "%q is not the landlord of lease %q", landlord, leaseID)
	}
	if l.Parent == "" {
		return newErr(ErrStateNotAllowed, "lease %q is already a direct lease", leaseID)
	}
	if l.Recognized {
		return newErr(ErrStateNotAllowed, "lease %q already recognized", leaseID)
	}
	l.Recognized = true
	s.commit(ns)
	return nil
}
