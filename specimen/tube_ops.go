package specimen

func rejectionReason(now int64, method string, hemolysis int, it *item) string {
	if now-it.collectedAt > it.requirement.MaxDeliverySeconds {
		return ReasonTimeout
	}
	if it.requirement.ColdRequired && method != TransportCold {
		return ReasonColdChain
	}
	if hemolysis > it.requirement.HemolysisTolerance {
		return ReasonHemolysis
	}
	return ""
}

func (s *System) signableItemsLocked(t *tube) []*item {
	items := make([]*item, 0, len(t.itemIDs))
	for _, itemID := range t.itemIDs {
		it := s.itemsByID[itemID]
		if it.tubeID == t.id && it.status == StatusCollected {
			items = append(items, it)
		}
	}
	return items
}

func (s *System) tubeHasSignableItemLocked(t *tube) bool {
	for _, itemID := range t.itemIDs {
		it := s.itemsByID[itemID]
		if it.tubeID == t.id && it.status == StatusCollected {
			return true
		}
	}
	return false
}

func (t *tube) collectedBoundLocked(items []*item) int64 {
	bound := items[0].collectedAt
	for _, it := range items[1:] {
		if it.collectedAt < bound {
			bound = it.collectedAt
		}
	}
	return bound
}
