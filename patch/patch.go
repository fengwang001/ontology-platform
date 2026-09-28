package patch

import "ontology/udiff"

type Options struct {
	Fuzz      int
	MaxBytes  int
	MaxHunks  int
}

func Diff(name string, a, b []byte, ctx, maxD int) ([]byte, error) { return nil, nil }

func Apply(a []byte, text []byte, opt Options) ([]byte, error) { return nil, nil }

func Reverse(p []byte, lim udiff.Limits) ([]byte, error) { return nil, nil }

type Store struct {
	docs map[string]*docState
}

type docState struct {
	text []byte
	ver  int
	log  []Commit
}

type Commit struct {
	Name   string
	Text   []byte
	OK     bool
	Reason error
}
