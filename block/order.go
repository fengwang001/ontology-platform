package block

import "fmt"

// OrderError reports a strict-order violation at a specific position.
type OrderError struct {
	Pos int
	A   string
	B   string
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("block: not strictly increasing at %d: %q >= %q", e.Pos, e.A, e.B)
}

// CheckOrder verifies ss is strictly increasing in byte order.
func CheckOrder(ss []string) error {
	for i := 1; i < len(ss); i++ {
		if ss[i-1] >= ss[i] {
			return &OrderError{Pos: i, A: ss[i-1], B: ss[i]}
		}
	}
	return nil
}
