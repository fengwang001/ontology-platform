package imaging

import "fmt"

func (o opRegDevice) String() string {
	return fmt.Sprintf("RegisterDevice(id=%s class=%d fs=%d)", o.req.ID, o.req.Class, o.req.FieldStrength)
}
func (o opRegDevice) Reason() string {
	return "CT 无场强；MR 场强为正整数"
}
