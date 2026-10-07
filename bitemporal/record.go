package bitemporal

// Record 是一条携带双时态信息的本体实例快照记录。
type Record struct {
	ID          string
	Valid       *Interval
	Transaction *Interval
}

// Validate 校验记录自身两条时间轴区间的自洽性。
//
// 属于判定固定优先级的第 1 级（最高）：只要记录自身携带的任一时间轴
// 区间不自洽，立即返回该错误，不继续做任何版本层面的判定。
// 该方法不修改记录。
func (r *Record) Validate() error {
	if r.Valid != nil {
		if err := ValidateInterval(ValidTime, *r.Valid); err != nil {
			return err
		}
	}
	if r.Transaction != nil {
		if err := ValidateInterval(TransactionTime, *r.Transaction); err != nil {
			return err
		}
	}
	return nil
}
