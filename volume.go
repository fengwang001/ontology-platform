package thinpool

type volume struct {
	name     string
	virtual  uint64
	reserved uint64
	used     uint64
	mapping  *blockMapping
}

type VolumeSnapshot struct {
	Virtual  uint64
	Reserved uint64
	Used     uint64
	Deficit  uint64
	Physical map[uint64]uint64
}
