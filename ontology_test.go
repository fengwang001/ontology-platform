package ontology_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/fold"
)

func foldedCount(t *testing.T, f *fold.Folder) int64 {
	t.Helper()
	v := reflect.ValueOf(f).Elem().FieldByName("lastRunes")
	return v.MethodByName("Load").Call(nil)[0].Int()
}

func TestFoldIdempotence(t *testing.T) {
	for _, tc := range []string{"ß", "İ", "straße", "STRASSE", "ÉéÇç"} {
		if got := fold.Fold(fold.Fold(tc)); got != fold.Fold(tc) {
			t.Fatalf("%q folded %q is not idempotent", tc, got)
		}
	}
}

func TestSinglePassRuneCount(t *testing.T) {
	for _, count := range []int{1000, 100000} {
		runes := make([]rune, count)
		alphabet := []rune{'a', 'Z', 'é', 'Ç', '中', 'ß'}
		for i := range runes {
			runes[i] = alphabet[i%len(alphabet)]
		}
		folder := fold.New()
		input := string(runes)
		_ = folder.Fold(input)
		if got := foldedCount(t, folder); got != int64(count) {
			t.Fatalf("processed %d runes for input of %d runes", got, count)
		}
	}
}

func TestLookupConsistency(t *testing.T) {
	for _, tc := range []struct{ first, second string }{
		{"Alpha", "ALPHA"}, {"café", "CAFÉ"}, {"straße", "STRASSE"},
	} {
		table := api.NewTable(32, 8)
		_ = table.Put(tc.first, "same")
		first, okFirst := table.Get(tc.first)
		second, okSecond := table.Get(tc.second)
		wantHit := fold.Fold(tc.first) == fold.Fold(tc.second)
		if okFirst != true || okSecond != wantHit || (wantHit && first != second) {
			t.Fatalf("%q/%q hit=%v want %v", tc.first, tc.second, okSecond, wantHit)
		}
	}
}

func TestOriginalKeyPreservation(t *testing.T) {
	table := api.NewTable(32, 8)
	for _, put := range []struct{ key, value string }{
		{"First", "1"}, {"FIRST", "2"}, {"fIrSt", "3"},
	} {
		if err := table.Put(put.key, put.value); err != nil {
			t.Fatal(err)
		}
	}
	if got := table.Keys(); len(got) != 1 || got[0] != "First" {
		t.Fatalf("keys=%v", got)
	}
	if value, ok := table.Get("first"); !ok || value != "3" {
		t.Fatalf("value=%v ok=%v", value, ok)
	}
}

func TestKeysOrderIndependent(t *testing.T) {
	left := api.NewTable(32, 8)
	right := api.NewTable(32, 8)
	leftKeys := []string{"Zeta", "apple", "Mango", "ä"}
	rightKeys := []string{"ä", "Mango", "apple", "Zeta"}
	for i := range leftKeys {
		_ = left.Put(leftKeys[i], i)
		_ = right.Put(rightKeys[i], i)
	}
	if got, want := left.Keys(), right.Keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v != %v", got, want)
	}
}

func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want error
	}{
		{"empty", "", api.ErrEmptyKey}, {"long", "ABCDEFGHI", api.ErrKeyTooLong},
	} {
		table := api.NewTable(8, 2)
		if err := table.Put(tc.key, 1); !errors.Is(err, tc.want) || len(table.Keys()) != 0 {
			t.Fatalf("%s err=%v keys=%v", tc.name, err, table.Keys())
		}
	}
	table := api.NewTable(8, 2)
	_ = table.Put("one", 1)
	_ = table.Put("two", 2)
	if err := table.Put("three", 3); !errors.Is(err, api.ErrTableFull) || len(table.Keys()) != 2 {
		t.Fatalf("full err=%v keys=%v", err, table.Keys())
	}
}

func TestSelfCheck(t *testing.T) {
	for _, tc := range []struct{ key string }{
		{"Alpha"}, {"CAFÉ"}, {"中"} ,
	} {
		table := api.NewTable(32, 8)
		if err := table.Put(tc.key, 1); err != nil || table.SelfCheck() != nil {
			t.Fatalf("key=%q err=%v check=%v", tc.key, err, table.SelfCheck())
		}
	}
}

func TestConcurrentQueries(t *testing.T) {
	for _, goroutines := range []int{16, 64} {
		table := api.NewTable(32, 8)
		_ = table.Put("Alpha", 1)
		_ = table.Put("Beta", 2)
		results := make([]string, goroutines)
		var wait sync.WaitGroup
		for i := range results {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				alpha, _ := table.Get("ALPHA")
				beta, _ := table.Get("beta")
				results[i] = fmt.Sprintf("%v|%v|%v", alpha, beta, table.Keys())
			}(i)
		}
		wait.Wait()
		for _, result := range results[1:] {
			if result != results[0] {
				t.Fatalf("concurrent results differ: %q != %q", result, results[0])
			}
		}
	}
}
