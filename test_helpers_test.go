package coercion

import "fmt"

func mismatchError(worker int, got any) error {
	return fmt.Errorf("worker %d got %#v", worker, got)
}
