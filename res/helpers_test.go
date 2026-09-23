package res

import "os"

// openProbe is a tiny test helper kept in its own file for line-budget
// clarity.
func openProbe(name string) (*os.File, error) { return os.Open(name) }
