package main

import (
	"context"
	"errors"
	"fmt"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	checks := map[string]func() error{}
	checks["acquire-release"] = func() error { return errors.New("skeleton") }
	checks["queue-when-empty"] = func() error { return errors.New("skeleton") }
	checks["cancel-grant-race"] = func() error { return errors.New("skeleton") }
	checks["cancel-counts"] = func() error { return errors.New("skeleton") }
	checks["double-release"] = func() error { return errors.New("skeleton") }
	checks["unknown-release"] = func() error { return errors.New("skeleton") }
	checks["fifo-order"] = func() error { return errors.New("skeleton") }
	checks["tryacquire-queue"] = func() error { return errors.New("skeleton") }
	checks["zero-capacity"] = func() error { return errors.New("skeleton") }
	checks["cancel-access-100"] = func() error { return errors.New("skeleton") }
	checks["cancel-access-10000"] = func() error { return errors.New("skeleton") }
	checks["concurrent-selfcheck"] = func() error { return errors.New("skeleton") }

	names := []string{
		"acquire-release", "queue-when-empty", "cancel-grant-race", "cancel-counts",
		"double-release", "unknown-release", "fifo-order", "tryacquire-queue",
		"zero-capacity", "cancel-access-100", "cancel-access-10000", "concurrent-selfcheck",
	}
	for _, name := range names {
		report(name, checks[name]() == nil)
	}
	_ = context.Background
}
