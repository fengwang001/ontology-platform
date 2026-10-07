package cascade

import "testing"

// CheckInvariants 仅供测试：校验索引与派生计数的一致性。
func (c *Controller) CheckInvariants(t *testing.T, ctx string) {
	t.Helper()
	c.checkInvariants(t, ctx)
}
