package lease

// consentBook owns landlord consent grants (one-shot and general).
//
// One-shot consents are keyed by the sublease id they authorize and may be
// granted before the sublease exists; each is consumed by exactly one
// established sublease. General consents are keyed by (root, tenant): the
// latest unrevoked grant at the time the sublease is initiated authorizes it.
// Revocation never touches already established subleases.
type consentBook struct{ items []*Consent }

func (b *consentBook) grantOneShot(root LeaseID, landlord Party, id LeaseID, now int) {
	b.items = append(b.items, &Consent{
		OneShot: true, Lease: id, Root: root, Landlord: landlord, Granted: now,
	})
}

// grantGeneral appends a fresh grant. An earlier revoked grant stays revoked
// but is superseded by the new grant.
func (b *consentBook) grantGeneral(root LeaseID, landlord, tenant Party, now int) {
	b.items = append(b.items, &Consent{
		OneShot: false, Tenant: tenant, Root: root, Landlord: landlord, Granted: now,
	})
}

// revokeGeneral marks the latest general grant to (root, tenant) revoked.
// It returns false if no live grant exists (state error for the caller).
func (b *consentBook) revokeGeneral(root LeaseID, landlord, tenant Party, now int) bool {
	var latest *Consent
	for _, c := range b.items {
		if !c.OneShot && c.Root == root && c.Tenant == tenant && c.Landlord == landlord &&
			!c.Revoked && (latest == nil || c.Granted > latest.Granted) {
			latest = c
		}
	}
	if latest == nil {
		return false
	}
	latest.Revoked = true
	return true
}

// authorize finds consent for a sublease initiated at now by parent.Tenant.
// One-shot wins (it names the exact sublease); otherwise the latest
// unrevoked general grant issued at or before now applies.
func (b *consentBook) authorize(root LeaseID, parent Lease, tenant Party, subID LeaseID, now int) (*Consent, bool) {
	var oneShot, general *Consent
	for _, c := range b.items {
		if c.Root != root || c.Granted > now {
			continue
		}
		if c.OneShot {
			if c.Lease == subID && !c.Consumed {
				oneShot = c
			}
			continue
		}
		if c.Tenant == parent.Tenant && !c.Revoked && c.Landlord == parent.Landlord &&
			(general == nil || c.Granted > general.Granted) {
			general = c
		}
	}
	if oneShot != nil {
		return oneShot, true
	}
	if general != nil {
		return general, true
	}
	return nil, false
}
