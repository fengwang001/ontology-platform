package leasechain

func gcKey(landlord, tenant string) string { return landlord + "\x00" + tenant }

// grantGeneral 记录一次概括性同意授予。
func (s *Service) grantGeneral(landlord, tenant string, at int64) {
	s.generals[gcKey(landlord, tenant)] = generalConsent{
		landlord: landlord, tenant: tenant, grantedAt: at,
	}
}

// revokeGeneral 记录一次概括性同意撤回（撤回只影响之后发起的转租）。
func (s *Service) revokeGeneral(landlord, tenant string, at int64) {
	k := gcKey(landlord, tenant)
	g := s.generals[k]
	if g.grantedAt == 0 || g.revokedAt != 0 {
		return
	}
	g.revokedAt = at
	s.generals[k] = g
}

// generalValidAt 判断 (landlord, tenant) 的概括性同意在 at 时是否有效。
func (s *Service) generalValidAt(landlord, tenant string, at int64) bool {
	g := s.generals[gcKey(landlord, tenant)]
	return g.grantedAt != 0 && g.revokedAt == 0
}

// findOneTime 查找匹配拟转租条款且未使用的一次性同意；返回 nil 表示无。
func (s *Service) findOneTime(landlord, tenant string, parentID int64, start, end int, rent int64) *OneTimeConsent {
	for _, c := range s.oneTimes {
		if c.Used || c.LandlordID != landlord || c.TenantID != tenant ||
			c.ParentID != parentID || c.Start != start || c.End != end || c.Rent != rent {
			continue
		}
		return c
	}
	return nil
}
