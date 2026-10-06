package toll

import (
	"sort"
	"time"
)

// TypeChange 是一次带生效时刻的车型变更。
type TypeChange struct {
	Effective time.Time
	Type      string
	Seq       int // 操作序号,同一生效时刻后到者覆盖先到者
}

// Vehicle 是车辆状态。
type Vehicle struct {
	ID          string
	InitialType string
	Changes     []TypeChange // 按 (Effective, Seq) 升序
	Journeys    []*Journey
	Open        *Journey               // 当前未结算且未人工关闭的行程
	kept        map[string][]time.Time // 门架 -> 已保留记录时刻(用于去重)
	Orphans     []*GantryRecord
}

func newVehicle(id, typ string) *Vehicle {
	return &Vehicle{
		ID:          id,
		InitialType: typ,
		kept:        make(map[string][]time.Time),
	}
}

// TypeAt 返回时刻 t 生效的车型;恰等于生效时刻按新车型。
func (v *Vehicle) TypeAt(t time.Time) string {
	typ := v.InitialType
	for _, c := range v.Changes {
		if c.Effective.After(t) {
			break
		}
		typ = c.Type
	}
	return typ
}

func (v *Vehicle) addTypeChange(c TypeChange) {
	v.Changes = append(v.Changes, c)
	sort.SliceStable(v.Changes, func(i, j int) bool {
		a, b := v.Changes[i], v.Changes[j]
		if !a.Effective.Equal(b.Effective) {
			return a.Effective.Before(b.Effective)
		}
		return a.Seq < b.Seq
	})
}

// isDuplicate 判断同门架是否存在间隔严格小于窗口的已保留记录。
// 间隔恰等于窗口视为两条独立记录。
func (v *Vehicle) isDuplicate(gantry string, t time.Time, window time.Duration) bool {
	for _, kt := range v.kept[gantry] {
		d := t.Sub(kt)
		if d < 0 {
			d = -d
		}
		if d < window {
			return true
		}
	}
	return false
}

func (v *Vehicle) keep(gantry string, t time.Time) {
	v.kept[gantry] = append(v.kept[gantry], t)
}

// Journey 是一次行程:入口记录开始、出口记录结束。
type Journey struct {
	ID          string
	Vehicle     *Vehicle
	Entry       *GantryRecord
	Exit        *GantryRecord
	Records     []*GantryRecord // 已登记的中间记录(含孤立/超期,推定时按状态过滤)
	Settled     bool
	Closed      bool // 人工关闭(路径不可达且未结算)
	Path        []string
	Fee         int64 // 当前推定路径的全额费用(未做封顶修正)
	Received    int64 // 当前实收(已扣除退款)
	Adjustments []*Adjustment
}
