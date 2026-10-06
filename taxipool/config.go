package taxipool

// Config 为蓄车池全部可调参数，时长与距离均为 int64，时刻单位由调用方自定。
type Config struct {
	Terminals         map[string]int // 候机楼标识 -> 队列容量
	ArriveLimit       int64          // 放行后到达上客点时限
	TransferLimit     int64          // 调剂放行后的到达时限
	NoShowLimit       int            // 爽约累计达到该次数即禁入
	BanDuration       int64          // 禁入时长
	ShortTripDistance int64          // 短途阈值（取等算短途）
	ReturnLimit       int64          // 短途返回时限（取等算及时）
	VoucherValidity   int64          // 凭证有效时长
	DailyVoucherLimit int            // 每司机每自然日凭证发放上限
	PriorityCap       int            // 每队列优先名额上限
	DayOffsetMinutes  int            // 自然日切分的时区偏移（分钟）
}

func (c Config) validate() error {
	if len(c.Terminals) == 0 {
		return ErrInvalidParam
	}
	for id, cap := range c.Terminals {
		if id == "" || cap <= 0 {
			return ErrInvalidParam
		}
	}
	if c.ArriveLimit <= 0 || c.TransferLimit <= 0 || c.NoShowLimit <= 0 ||
		c.BanDuration < 0 || c.ShortTripDistance < 0 || c.ReturnLimit < 0 ||
		c.VoucherValidity < 0 || c.DailyVoucherLimit < 0 || c.PriorityCap < 0 {
		return ErrInvalidParam
	}
	if c.DayOffsetMinutes < -720 || c.DayOffsetMinutes > 840 {
		return ErrInvalidParam
	}
	return nil
}

// dayOf 按配置时区把时刻归入自然日（地板除，支持负偏移）。
func dayOf(ts int64, offsetMinutes int) int64 {
	v := ts + int64(offsetMinutes)*60
	q := v / 86400
	if v < 0 && v%86400 != 0 {
		q--
	}
	return q
}
