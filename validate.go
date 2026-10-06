package hospital

import "fmt"

func invalidf(format string, args ...any) error {
	return &OperationError{Code: ErrInvalidArgument, Reason: fmt.Sprintf(format, args...)}
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &OperationError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

func nonEmpty(value, name string) error {
	if value == "" {
		return invalidf("%s 不能为空", name)
	}
	return nil
}

func validQty(qty int) error {
	if qty < 1 || qty > 1_000_000 {
		return invalidf("数量必须位于 1 到 1000000 之间")
	}
	return nil
}

func validNow(now int64) error {
	if now < 0 || now > 1_000_000_000 {
		return invalidf("now 必须位于 0 到 1000000000 之间")
	}
	return nil
}

func validPosition(position, name string) error {
	if err := nonEmpty(position, name); err != nil {
		return err
	}
	return nil
}

func (s *System) checkClock(now int64) error {
	if now < s.clock {
		return errorf(ErrClockRollback, "now=%d 小于已接受时钟 %d", now, s.clock)
	}
	return nil
}

func (s *System) validateCommon(now int64, drug, batch string, qty int) error {
	if err := nonEmpty(drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(batch, "批号"); err != nil {
		return err
	}
	if err := validQty(qty); err != nil {
		return err
	}
	if err := validNow(now); err != nil {
		return err
	}
	return nil
}
