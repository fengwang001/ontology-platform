package van

// zone 单个分区的运行时聚合状态。
type zone struct {
	weightLimit int
	volumeLimit int
	weight      int
	volume      int
	minStop     int
	maxStop     int
	has         [4]bool
	items       map[int]Cargo
}

func newZone(weightLimit, volumeLimit int) *zone {
	return &zone{
		weightLimit: weightLimit,
		volumeLimit: volumeLimit,
		minStop:     0,
		maxStop:     0,
		items:       make(map[int]Cargo),
	}
}

// put 必须在判定通过后调用；仅更新聚合，复杂度 O(1)。
func (z *zone) put(c Cargo) {
	z.items[c.ID] = c
	z.weight += c.Weight
	z.volume += c.Volume
	if z.minStop == 0 || c.Stop < z.minStop {
		z.minStop = c.Stop
	}
	if c.Stop > z.maxStop {
		z.maxStop = c.Stop
	}
	z.has[c.Kind] = true
}

// remove 删除指定货物并重建聚合；只在卸货时使用。
func (z *zone) remove(id int) {
	delete(z.items, id)
	z.rebuild()
}

func (z *zone) rebuild() {
	z.weight = 0
	z.volume = 0
	z.minStop = 0
	z.maxStop = 0
	z.has = [4]bool{}
	for _, c := range z.items {
		z.weight += c.Weight
		z.volume += c.Volume
		if z.minStop == 0 || c.Stop < z.minStop {
			z.minStop = c.Stop
		}
		if c.Stop > z.maxStop {
			z.maxStop = c.Stop
		}
		z.has[c.Kind] = true
	}
}

func (z *zone) empty() bool { return len(z.items) == 0 }
