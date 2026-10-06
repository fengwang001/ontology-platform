package locker

import "fmt"

// 以下函数只负责把操作输入/输出/判定依据格式化为日志文本。

func depositInput(t int64, tracking TrackingNo, size Size, phone Phone) string {
	return fmt.Sprintf("t=%d tracking=%q size=%s phone=%q", t, tracking, sizeName(size), phone)
}

func pickupInput(t int64, code Code, phone Phone) string {
	return fmt.Sprintf("t=%d code=%s phone=%q", t, code, phone)
}

func payInput(t int64, tracking TrackingNo, amount int64) string {
	return fmt.Sprintf("t=%d tracking=%q amount=%d", t, tracking, amount)
}

func trackingInput(_ string, t int64, tracking TrackingNo) string {
	return fmt.Sprintf("t=%d tracking=%q", t, tracking)
}

func sizeName(s Size) string {
	switch s {
	case SizeSmall:
		return "S"
	case SizeMedium:
		return "M"
	case SizeLarge:
		return "L"
	default:
		return "?"
	}
}

func fmtOutput(out any) string { return fmt.Sprintf("%+v", out) }

// rejectReason 给出被拒绝的判定依据（对应优先级链路上的具体一环）。
func rejectReason(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidParam:
		return "parameter validation failed (negative time / empty tracking / bad size or phone)"
	case ErrClockRollback:
		return "t is earlier than the last accepted operation time"
	case ErrDuplicateTracking:
		return "same tracking number is already in the cabinet"
	case ErrNoFittingCell:
		return "no cell of sufficient size exists anywhere (checked before occupancy)"
	case ErrAllFittingBusy:
		return "fitting cells exist but all are currently occupied"
	case ErrNoCodeAvailable:
		return "all codes are active or still cooling"
	case ErrCodeNotFound:
		return "code is unknown or its parcel was removed/recycled"
	case ErrParcelLocked:
		return "parcel is locked after 3 consecutive phone mismatches; needs operator unlock"
	case ErrPhoneMismatch:
		return "last-four digits differ; mismatch counted but clock not advanced"
	case ErrTimedOut:
		return "storage duration reached MaxStorage; pickup blocked, awaiting recycling"
	case ErrUnpaidFee:
		return "storage fee due at t is not fully paid"
	case ErrTrackingNotFound:
		return "tracking number is not currently in the cabinet"
	case ErrNotTimedOut:
		return "only timed-out parcels may be recycled"
	default:
		return err.Error()
	}
}

func acceptReason(op string, out any) string {
	switch op {
	case "Deposit":
		return "smallest fitting free cell chosen by hierarchical bitset; smallest reusable code assigned"
	case "Pickup":
		return "code+phone matched, in time and fee settled; cell released and code cooling"
	case "Pay":
		return "payment recorded against fee due"
	case "Recycle":
		return "timed-out parcel recycled; cell released and code cooling"
	case "Unlock":
		return "lock cleared and mismatch counter reset"
	default:
		return ""
	}
}

// Due 返回 t 时刻指定运单的尚欠金额（只读，不推进时钟）；运单不存在时 ok=false。
func (c *Cabinet) Due(t int64, tracking TrackingNo) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.active[tracking]
	if p == nil {
		return 0, false
	}
	return p.due(c.cfg, t), true
}

// Occupied 返回当前柜内快件清单的只读快照（运单 -> 格口）。
func (c *Cabinet) Occupied() map[TrackingNo]CellID {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[TrackingNo]CellID, len(c.active))
	for k, p := range c.active {
		snap[k] = p.cell
	}
	return snap
}
