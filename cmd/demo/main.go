// Command demo exercises the cursor-based paginated traversal of package
// ontology and prints one OK/FAIL verdict per scenario.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology"
)

var passed, failed int

func check(ok bool, label string) {
	if ok {
		passed++
		fmt.Println("OK   " + label)
	} else {
		failed++
		fmt.Println("FAIL " + label)
	}
}

func keysOf(items []ontology.Object) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return keys
}

func has(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func main() {
	s := ontology.NewStore()
	want := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("k%02d", i)
		s.Put(k, i)
		want = append(want, k)
	}

	// 1. Full traversal in three pages: no duplicates, no misses.
	var got []string
	cursor, pages := "", 0
	var last ontology.Page
	for {
		p, err := s.Scan(cursor, 4)
		if err != nil {
			break
		}
		got = append(got, keysOf(p.Items)...)
		pages++
		cursor, last = p.Next, p
		if !p.HasMore {
			break
		}
	}
	tail, tailErr := s.Scan(cursor, 4)
	check(pages == 3 && reflect.DeepEqual(got, want) && !last.HasMore &&
		tailErr == nil && len(tail.Items) == 0 && !tail.HasMore,
		"3-page scan: 10 items, no dup/miss, empty tail page")

	// 2. Mutations mid-traversal: insert before position, delete after it.
	p1, _ := s.Scan("", 3) // k00 k01 k02
	s.Put("a99", 99)       // sorts before the current position
	s.Delete("k07")        // not yet scanned
	seen := keysOf(p1.Items)
	dropped := 0
	cur := p1.Next
	for {
		p, err := s.Scan(cur, 3)
		if err != nil {
			break
		}
		seen = append(seen, keysOf(p.Items)...)
		dropped += p.Dropped
		cur = p.Next
		if !p.HasMore {
			break
		}
	}
	st, stErr := s.TraversalStats(cur)
	check(stErr == nil && len(seen) == 9 && !has(seen, "a99") && !has(seen, "k07") &&
		st.MutatedByInsert && st.MutatedByDelete &&
		st.SkippedInserted == 1 && st.SkippedDeleted == 1 && dropped == 1,
		"mid-scan insert+delete: flags set, skipped ins=1 del=1, dropped=1")

	// 3. limit boundaries: 0 vs negative are distinct outcomes.
	z, errZ := s.Scan("", 0)
	z2, _ := s.Scan(z.Next, 0)
	_, errNeg := s.Scan("", -1)
	check(errZ == nil && len(z.Items) == 0 && z2.Next == z.Next &&
		errors.Is(errNeg, ontology.ErrNegativeLimit) &&
		!errors.Is(errNeg, ontology.ErrInvalidCursor),
		"limit=0 empty page/no advance vs limit=-1 argument error")

	// 4. Tampered cursor vs invalidated session: distinct error categories.
	good, _ := s.Scan("", 5)
	raw := []byte(good.Next)
	if raw[10] == 'A' {
		raw[10] = 'B'
	} else {
		raw[10] = 'A'
	}
	_, errTamper := s.Scan(string(raw), 5)
	other, _ := s.Scan("", 5)
	_ = s.Invalidate(other.Next)
	_, errSess := s.Scan(other.Next, 5)
	check(errors.Is(errTamper, ontology.ErrInvalidCursor) &&
		errors.Is(errSess, ontology.ErrInvalidSession) &&
		!errors.Is(errSess, ontology.ErrInvalidCursor),
		"tampered cursor vs invalidated session: distinct errors")

	// 5. Concurrent Scan of the same cursor is idempotent.
	base, _ := s.Scan("", 4)
	var res [2]ontology.Page
	var errs [2]error
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], errs[i] = s.Scan(base.Next, 4)
		}(i)
	}
	wg.Wait()
	check(errs[0] == nil && errs[1] == nil &&
		reflect.DeepEqual(res[0].Items, res[1].Items) && res[0].Next == res[1].Next,
		"concurrent Scan of same cursor: identical pages")

	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
