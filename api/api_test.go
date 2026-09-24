package api_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ontology/api"
	"ontology/fold"
	"ontology/keymap"
)

// buildStr 生成恰含 n 个 rune 的串（ASCII、变音符拉丁字母、多字节字符）。
func buildStr(n int) string {
	return string([]rune(strings.Repeat("aAéß中İ", n/6+1))[:n])
}
func TestFoldIdempotent(t *testing.T) {
	for _, s := range []string{"STRASSE", "straße", "İ", "ß", "ÀbÇ"} {
		if f := fold.Fold(s); fold.Fold(f) != f {
			t.Errorf("not idempotent for %q", s)
		}
	}
}
func TestFoldRuneCount(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		if _, got := fold.FoldCount(buildStr(n)); got != n {
			t.Errorf("processed %d runes, want %d", got, n)
		}
	}
}
func TestHitConsistency(t *testing.T) {
	api.Init(64, 100)
	api.Put("Hello", "1")
	api.Put("straße", "2")
	want := map[string]string{"hello": "1", "HELLO": "1", "HeLLo": "1", "STRASSE": ""}
	for k, v := range want {
		if got, _ := api.Get(k); got != v {
			t.Errorf("Get(%q)=%q, want %q", k, got, v)
		}
	}
}
func TestKeysFidelityAndOrder(t *testing.T) {
	for _, keys := range [][]string{{"alpha", "Beta", "GAMMA"}, {"GAMMA", "alpha", "Beta"}, {"GAMMA", "Beta", "alpha"}} {
		api.Init(64, 100)
		for _, k := range keys {
			api.Put(k, "v")
		}
		api.Put("BETA", "v2") // 重复 Put：覆盖值、保留首次写法
		v, _ := api.Get("beta")
		if got := api.Keys(); !slices.Equal(got, []string{"alpha", "Beta", "GAMMA"}) || v != "v2" {
			t.Errorf("Keys()=%v Get=%q, want first-spelling order and v2", got, v)
		}
	}
}
func TestRejectLeavesNoTrace(t *testing.T) {
	api.Init(4, 2)
	api.Put("ab", "1")
	api.Put("cd", "2")
	before := api.Keys()
	for key, want := range map[string]error{"": keymap.ErrEmptyKey, "abcde": keymap.ErrKeyTooLong, "ef": keymap.ErrTooManyEntries} {
		if err := api.Put(key, "x"); err != want {
			t.Errorf("Put(%q) err=%v, want %v", key, err, want)
		}
	}
	if got := api.Keys(); !slices.Equal(got, before) || api.Put("AB", "9") != nil {
		t.Errorf("table mutated or unusable after rejects: %v", got)
	}
}
func TestSelfCheck(t *testing.T) {
	api.Init(64, 100)
	api.Put("One", "1")
	if err := api.SelfCheck(); err != nil || len(api.Keys()) != 1 {
		t.Fatal("self-check failed")
	}
}
func TestConcurrentReads(t *testing.T) {
	api.Init(64, 100)
	for i := 0; i < 8; i++ {
		api.Put(fmt.Sprintf("Key%d", i), "v")
	}
	want := api.Keys()
	start, results := make(chan struct{}), make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			<-start
			ok := slices.Equal(api.Keys(), want) && api.SelfCheck() == nil
			for i := 0; i < 8 && ok; i++ {
				_, ok = api.Get(fmt.Sprintf("kEy%d", i))
			}
			results <- ok
		}()
	}
	close(start)
	for n := 0; n < 8; n++ {
		if !<-results {
			t.Error("inconsistent concurrent read")
		}
	}
}
