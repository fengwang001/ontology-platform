package taxipool

import (
	"fmt"
	"time"
)

// Config 是蓄车池的全部可调参数。
type Config struct {
	// Terminals：候机楼标识 -> 队列容量（必须 > 0）。
	Terminals map[string]int
	// ArrivalLimit：本候机楼放行后到达上客点的时限。
	ArrivalLimit time.Duration
	// TransferLimit：跨候机楼调剂放行后的到达时限。
	TransferLimit time.Duration
	// ShortTripMeters：短途阈值，取等算短途。
	ShortTripMeters float64
	// ReturnLimit：从离开到再次入池的返回时限，取等算及时。
	ReturnLimit time.Duration
	// VoucherTTL：凭证自发放起的有效时长。
	VoucherTTL time.Duration
	// DailyVoucherLimit：每位司机每个自然日最多获得的凭证数。
	DailyVoucherLimit int
	// PrioritySlots：每条队列同时允许的优先司机名额上限。
	PrioritySlots int
	// NoShowLimit：累计爽约达到该次数即触发禁入。
	NoShowLimit int
	// BanDuration：禁入时长，禁入期恰结束那一刻可入池。
	BanDuration time.Duration
	// TimeZone：自然日切分时区。
	TimeZone *time.Location
}

// Validate 校验配置；非法配置返回包裹 ErrInvalidParam 的错误。
func (c *Config) Validate() error {
	bad := func(msg string) error {
		return &OpError{Kind: KindInvalidParam, Op: "Config.Validate", Detail: msg}
	}
	if c == nil {
		return bad("配置为空")
	}
	if len(c.Terminals) == 0 {
		return bad("至少需要一个候机楼")
	}
	for id, capv := range c.Terminals {
		if id == "" {
			return bad("候机楼标识不能为空")
		}
		if capv <= 0 {
			return bad(fmt.Sprintf("候机楼 %s 容量必须为正: %d", id, capv))
		}
	}
	if c.ArrivalLimit <= 0 {
		return bad("ArrivalLimit 必须为正")
	}
	if c.TransferLimit <= 0 {
		return bad("TransferLimit 必须为正")
	}
	if c.ReturnLimit < 0 {
		return bad("ReturnLimit 不能为负")
	}
	if c.VoucherTTL <= 0 {
		return bad("VoucherTTL 必须为正")
	}
	if c.ShortTripMeters < 0 {
		return bad("ShortTripMeters 不能为负")
	}
	if c.DailyVoucherLimit <= 0 {
		return bad("DailyVoucherLimit 必须为正")
	}
	if c.PrioritySlots <= 0 {
		return bad("PrioritySlots 必须为正")
	}
	if c.NoShowLimit <= 0 {
		return bad("NoShowLimit 必须为正")
	}
	if c.BanDuration < 0 {
		return bad("BanDuration 不能为负")
	}
	if c.TimeZone == nil {
		return bad("TimeZone 不能为空")
	}
	return nil
}

// dayKey 返回某时刻在配置时区下归属的自然日键。
func (c *Config) dayKey(t time.Time) string {
	y, m, d := t.In(c.TimeZone).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}
