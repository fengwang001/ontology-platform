package exports

import (
	"reflect"
	"sync"
	"testing"
)

// 两套表：键相同、目标不同，便于判定每次解析看到的是哪张表。
var (
	oldEntries = []Entry{
		{Key: "./*", Target: StringTarget("./old/*")},
		{Key: "./exact", Target: StringTarget("./old-exact.js")},
		{Key: "./c", Target: ConditionsTarget(
			Cond("feat", StringTarget("./old-feat.js")),
			Cond("default", StringTarget("./old-def.js")),
		)},
	}
	newEntries = []Entry{
		{Key: "./*", Target: StringTarget("./new/*")},
		{Key: "./exact", Target: StringTarget("./new-exact.js")},
		{Key: "./c", Target: ConditionsTarget(
			Cond("feat", StringTarget("./new-feat.js")),
			Cond("default", StringTarget("./new-def.js")),
		)},
	}
)

func TestConcurrentReplaceAndResolve(t *testing.T) {
	r := NewResolver(mustTable(t, oldEntries...))

	requests := []struct {
		subpath string
		conds   []string
	}{
		{"./a/b", nil},
		{"./exact", nil},
		{"./c", []string{"feat"}},
		{"./c", nil},
		{"./a/node_modules/x", nil},
		{"./missing/deep/path", nil},
	}

	type outcome struct {
		res Result
		err *Error
	}
	expects := make([][2]outcome, len(requests))
	for i, req := range requests {
		for k, entries := range [][]Entry{oldEntries, newEntries} {
			res, err := naiveResolve(entries, req.subpath, req.conds)
			expects[i][k].res = res
			if err != nil {
				expects[i][k].err = err.(*Error)
			}
		}
	}

	stop := make(chan struct{})
	var replacerWg sync.WaitGroup
	replacerWg.Add(1)
	go func() {
		defer replacerWg.Done()
		flip := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			var err error
			if flip {
				err = r.Replace(oldEntries)
			} else {
				err = r.Replace(newEntries)
			}
			if err != nil {
				t.Errorf("Replace: %v", err)
				return
			}
			flip = !flip
		}
	}()

	const workers = 8
	const iters = 2000
	var resolverWg sync.WaitGroup
	for w := 0; w < workers; w++ {
		resolverWg.Add(1)
		go func(seed int) {
			defer resolverWg.Done()
			for it := 0; it < iters; it++ {
				i := (seed + it) % len(requests)
				req := requests[i]
				res, err := r.Resolve(req.subpath, req.conds)
				matched := false
				for k := 0; k < 2; k++ {
					exp := expects[i][k]
					if exp.err != nil {
						if e, ok := err.(*Error); ok && e.Kind == exp.err.Kind {
							matched = true
							break
						}
						continue
					}
					if err == nil && reflect.DeepEqual(res, exp.res) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("req=%q conds=%v: result %+v err=%v matches neither old nor new table",
						req.subpath, req.conds, res, err)
					return
				}
			}
		}(w)
	}

	resolverWg.Wait()
	close(stop)
	replacerWg.Wait()
}
