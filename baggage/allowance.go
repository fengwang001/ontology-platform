package baggage

// AllowanceCarrier 确定一段行程的适用额度承运人：
// 存在跨区域航段时取第一个跨区域航段的承运人，否则取第一航段的承运人。
// 纯函数，开销 O(航段数)。
func AllowanceCarrier(segs []Segment, airports map[string]Airport) string {
	for _, seg := range segs {
		if airports[seg.From].Region != airports[seg.To].Region {
			return seg.Carrier
		}
	}
	return segs[0].Carrier
}
