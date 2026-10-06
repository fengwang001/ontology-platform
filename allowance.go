package baggage

// applicableCarrierID 返回一程行程适用的承运人：首个跨区域航段承运人，否则首段承运人。
// 复杂度 O(段数)，每个机场仅 O(1) 查表，与机场总数无关。
func applicableCarrierID(segs []Segment, reg *Registry) string {
	for i := range segs {
		from, _ := reg.airport(segs[i].From)
		to, _ := reg.airport(segs[i].To)
		if from.Region != to.Region {
			return segs[i].CarrierID
		}
	}
	return segs[0].CarrierID
}
