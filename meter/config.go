package meter

type Config struct {
	CreatedAt Tick

	WarnThreshold      Money
	EmergencyThreshold Money
	EmergencyAmount    Money
	RestoreThreshold   Money
	DebtRepayRatio     Ratio
	ConfirmWindow      int64

	CycleLength int64

	FriendlyStart int64
	FriendlyEnd   int64
	Holidays      map[int64]struct{}

	Tariffs []Tariff
}

const daySeconds int64 = 86400

type Tariff struct {
	Start Tick
	Price int64
}

func (r Ratio) valid() bool {
	return r.D > 0 && r.N >= 0 && r.N <= r.D
}

func (cfg Config) validate(initialBalance Money) error {
	if cfg.CycleLength <= 0 {
		return ErrInvalidParam
	}
	if cfg.WarnThreshold <= 0 {
		return ErrInvalidParam
	}
	if cfg.RestoreThreshold < 0 {
		return ErrInvalidParam
	}
	if cfg.EmergencyAmount <= 0 {
		return ErrInvalidParam
	}
	if cfg.ConfirmWindow <= 0 {
		return ErrInvalidParam
	}
	if !cfg.DebtRepayRatio.valid() {
		return ErrInvalidParam
	}
	if cfg.FriendlyStart < 0 || cfg.FriendlyEnd < 0 ||
		cfg.FriendlyStart >= daySeconds || cfg.FriendlyEnd > daySeconds {
		return ErrInvalidParam
	}
	if cfg.FriendlyStart > 0 && cfg.FriendlyStart >= cfg.FriendlyEnd {
		return ErrInvalidParam
	}
	if len(cfg.Tariffs) == 0 {
		return ErrInvalidParam
	}
	for i, tf := range cfg.Tariffs {
		if tf.Price < 0 {
			return ErrInvalidParam
		}
		if i > 0 && tf.Start <= cfg.Tariffs[i-1].Start {
			return ErrInvalidParam
		}
	}
	if cfg.Tariffs[0].Start > cfg.CreatedAt {
		return ErrInvalidParam
	}
	return nil
}

// priceAt 返回 t 时刻生效的电价：电价在 Start 时刻生效，
// 已计费区间不受后续变更影响。tariffs 有序，二分查找，O(log n)。
func (cfg Config) priceAt(t Tick) int64 {
	lo, hi := 0, len(cfg.Tariffs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if cfg.Tariffs[mid].Start <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return cfg.Tariffs[lo-1].Price
}

// isRestDayOrHoliday O(1) 判定：节假日哈希 + 周末取模，
// 不随节假日总数增长。day 0 = 1970-01-01（星期四）。
func (cfg Config) isRestDayOrHoliday(t Tick) bool {
	day := floorDiv(t, daySeconds)
	if _, ok := cfg.Holidays[day]; ok {
		return true
	}
	weekday := floorMod(day+3, 7) // 0=星期一 …… 5=星期六 6=星期日
	return weekday >= 5
}

// inFriendly 左闭右开判定友好时段。
func (cfg Config) inFriendly(t Tick) bool {
	day := floorDiv(t, daySeconds)
	dayStart := day * daySeconds
	off := t - dayStart
	if cfg.isRestDayOrHoliday(t) {
		return true
	}
	return cfg.FriendlyStart <= off && off < cfg.FriendlyEnd
}

// friendlyEnd 返回包含 t 的当前友好时段的结束时刻。
// 休息日/节假日为全天；每日窗口结束于当天 FriendlyEnd。
func (cfg Config) friendlyEnd(t Tick) Tick {
	day := floorDiv(t, daySeconds)
	dayStart := day * daySeconds
	if cfg.isRestDayOrHoliday(t) {
		return dayStart + daySeconds
	}
	return dayStart + cfg.FriendlyEnd
}

// cycleIndexAt 周期自 CreatedAt 起按固定长度锚定。
func (cfg Config) cycleIndexAt(t Tick) int64 {
	return floorDiv(t-cfg.CreatedAt, cfg.CycleLength)
}

func (cfg Config) cycleBounds(idx int64) (Tick, Tick) {
	start := cfg.CreatedAt + idx*cfg.CycleLength
	return start, start + cfg.CycleLength
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 {
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		r += b
	}
	return r
}
