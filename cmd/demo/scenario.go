package main

import (
	"ontology/merge"
	"ontology/source"
)

const demoFile = "greeting = hello ${name}\nname = file\nwho = f0\n.\n"

// builders construct each source independently so construction order can be
// shuffled while merge priority stays fixed.
var builders = []func() ([]source.Entry, error){
	func() ([]source.Entry, error) {
		return source.FromPairs(source.Default, map[string]string{
			"greeting": "hi ${name}", "name": "def", "who": "d0",
		})
	},
	func() ([]source.Entry, error) {
		return source.ParseFile(source.File, []byte(demoFile))
	},
	func() ([]source.Entry, error) {
		return source.FromEnv(source.Env, []string{"NAME=env", "WHO=e0"}, "")
	},
	func() ([]source.Entry, error) {
		return source.ParseArgs(source.CLI, []string{"--who=c0"})
	},
}

// layers builds the four sources in the given construction order.
func layers(order []int) ([][]source.Entry, error) {
	out := make([][]source.Entry, len(builders))
	for _, i := range order {
		entries, err := builders[i]()
		if err != nil {
			return nil, err
		}
		out[i] = entries
	}
	return out, nil
}

func merged() (*merge.Result, error) {
	l, err := layers([]int{0, 1, 2, 3})
	if err != nil {
		return nil, err
	}
	return merge.Merge(l...), nil
}
