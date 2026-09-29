package di

import "fmt"

// safeCall invokes a constructor, converting panics into errors so a
// panicking constructor cannot leak the partially built dependency chain.
func safeCall(reg *registration, deps map[string]any) (instance any, release func(), err error) {
	defer func() {
		if r := recover(); r != nil {
			instance = nil
			release = nil
			err = fmt.Errorf("%v", r)
		}
	}()
	instance, release = reg.ctor(deps)
	return instance, release, nil
}
