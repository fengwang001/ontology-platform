// Package decider 按固定次序 D1→D4 评估一次放置是否可行，返回第一个否决者。
package decider

// Reason 是评估结论；空串表示通过。
type Reason string

const (
	Pass            Reason = ""
	D1Excluded      Reason = "D1_excluded"
	D2SameShard     Reason = "D2_same_shard"
	D3ZoneAwareness Reason = "D3_zone_awareness"
	D4Disk          Reason = "D4_disk"
)

// String 便于日志输出判定依据。
func (r Reason) String() string {
	if r == Pass {
		return "pass"
	}
	return string(r)
}

// View 是决策器需要的只读集群视图。
// 迁出场景由实现方在 ZoneCopyCount/Used/HasShardCopy 中体现"先从源拿走一份"的覆盖。
type View interface {
	Excluded(nid string) bool
	HasShardCopy(nid, index string, s int) bool
	ZoneCopyCount(zone, index string, s int) int
	ZoneCount() int
	Used(nid string) int64
	Total(nid string) int64
	ZoneOf(nid string) string
}

// Params 是一次评估的固定输入。
type Params struct {
	Index    string
	Shard    int
	Copies   int   // c = 1 + r
	Size     int64 // 每份字节数
	Primary  bool  // 主还是副
	LowWater bool  // true：D4 一律按低水位（迁出场景）
	L, H     int   // 低、高水位百分数
}

// Evaluate 评估把 p 描述的一份放到节点 nid，按 D1、D2、D3、D4 次序返回第一个否决者。
func Evaluate(v View, nid string, p Params) Reason {
	// D1 排除
	if v.Excluded(nid) {
		return D1Excluded
	}
	// D2 同分片
	if v.HasShardCopy(nid, p.Index, p.Shard) {
		return D2SameShard
	}
	// D3 区域感知：zone 内已有份数 + 1 > ceil(c/z) 则否决
	z := v.ZoneCount()
	zone := v.ZoneOf(nid)
	limit := (p.Copies + z - 1) / z
	if v.ZoneCopyCount(zone, p.Index, p.Shard)+1 > limit {
		return D3ZoneAwareness
	}
	// D4 磁盘：恰等不否决
	water := p.L
	if p.Primary && !p.LowWater {
		water = p.H
	}
	if (v.Used(nid)+p.Size)*100 > int64(water)*v.Total(nid) {
		return D4Disk
	}
	return Pass
}
