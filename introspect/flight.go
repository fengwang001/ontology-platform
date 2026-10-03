package introspect

type flight struct {
	done chan struct{}
	res  Result
	err  error
}
