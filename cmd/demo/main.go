package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func evolved() *api.Ontology {
	o := api.New(
		api.Column{Name: "x", Typ: 0, Required: true},
		api.Column{Name: "y", Typ: 1, Required: true},
		api.Column{Name: "m", Typ: 1, Required: true},
	)
	o.AddColumn("z", "str", true)
	o.ChangeType("y", "int")
	o.ChangeType("x", "str")
	o.DropColumn("m")
	o.AddColumn("w", "int", false)
	return o
}

func main() {
	o := evolved()
	check("selfcheck", o.SelfCheck() == nil)

	// The six events from NOTES.md, mapped to active [x:str,y:int,z:str,w:int].
	evs := []struct {
		ver  int
		vals []any
		want string
	}{
		{6, []any{"1", int64(2), "a", int64(3)}, "[1 2 a 3]"},
		{2, []any{int64(5), "6", "M", "b"}, "[5 6 b 0]"}, // y=6, w=0
		{2, []any{int64(5), "abc", "M", "b"}, "ErrBadValue"},
		{4, []any{"9", int64(10), "M", "d"}, "[9 10 d 0]"}, // w=0
		{1, []any{int64(11), "12", "M"}, "ErrMissingColumn"},
		{3, []any{int64(13), int64(14), "M", "e"}, "[13 14 e 0]"},
	}
	ok := true
	for _, e := range evs {
		got, err := o.Map(e.ver, e.vals)
		s := fmt.Sprint(got)
		switch err {
		case nil:
		case api.ErrBadValue:
			s = "ErrBadValue"
		case api.ErrMissingColumn:
			s = "ErrMissingColumn"
		}
		ok = ok && s == e.want
	}
	check("six events", ok)

	// Four decidable, mutually distinct error classes.
	e1 := o.AddColumn("q", "float", false) // bad type
	e2 := o.AddColumn("", "int", false)    // empty name
	e3 := o.AddColumn("x", "int", false)   // duplicate
	e4 := o.ChangeType("nope", "int")      // unknown column
	_, e5 := o.Map(99, nil)                // unregistered version
	ok = e1 == api.ErrBadType && e2 == api.ErrEmptyName && e3 == api.ErrDuplicate &&
		e4 == api.ErrUnknownColumn && e5 == api.ErrUnknownVersion
	check("four error classes", ok)

	// Rejected ops leave state untouched; still usable.
	_, err := o.Map(6, []any{"1", int64(2), "a", int64(3)})
	ok = fmt.Sprint(o.Active()) == fmt.Sprint(evolved().Active()) && err == nil
	check("state unchanged", ok)

	// Large m: map a full-width event at several widths (per-column locate
	// comparison count is asserted O(1) in mapc's internal test).
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		seed := make([]api.Column, m)
		vals := make([]any, m)
		for i := range seed {
			seed[i] = api.Column{Name: fmt.Sprint("c", i), Typ: 0, Required: true}
			vals[i] = int64(i)
		}
		got, err := api.New(seed...).Map(1, vals)
		ok = ok && err == nil && len(got) == m
	}
	check("large-m map", ok)

	// Concurrent Map while a writer bumps versions: every result matches
	// either the old or the new schema, never a mix.
	c := api.New(api.Column{Name: "a", Typ: 0, Required: true})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // writer: keep bumping versions
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.AddColumn(fmt.Sprint("w", i), "int", false)
		}
		close(stop)
	}()
	ok = true
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := c.Map(1, []any{int64(7)})
				if err != nil || got[0] != int64(7) {
					ok = false
					return
				}
				for _, v := range got[1:] { // appended optional cols are zero
					if v != int64(0) {
						ok = false
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent map", ok)

	if failed {
		os.Exit(1)
	}
	fmt.Println("OK overall")
}
