// Command demo walks through the main features of the layered, versioned
// configuration system and prints the observable results.
package main

import (
	"fmt"

	"ontology/layerconfig"
)

func main() {
	s := layerconfig.NewStore()

	must(s.RegisterKey(layerconfig.Schema{Key: "timeout_ms", Type: layerconfig.TypeInt,
		Required: true, Min: 0, Max: 1_000_000, Merge: layerconfig.MergeOverride}))
	must(s.RegisterKey(layerconfig.Schema{Key: "features", Type: layerconfig.TypeStringList,
		Merge: layerconfig.MergeAppend}))

	pub(s, []layerconfig.Change{
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "timeout_ms", Value: layerconfig.Value{Int: 1000}},
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "features", Value: layerconfig.Value{List: []string{"base"}}},
	})
	pub(s, []layerconfig.Change{
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod"}, Key: "timeout_ms", Value: layerconfig.Value{Int: 250}},
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod"}, Key: "features", Value: layerconfig.Value{List: []string{"prod", "base"}}},
	})
	pub(s, []layerconfig.Change{
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn"}, Key: "features", Value: layerconfig.Value{List: []string{"cn"}}},
	})

	show(s, layerconfig.Scope{}, "timeout_ms")
	show(s, layerconfig.Scope{Env: "prod"}, "timeout_ms")
	show(s, layerconfig.Scope{Env: "prod", Region: "cn"}, "features")

	// Explicit cancellation: env prod voids global and env, narrower rewrite.
	pub(s, []layerconfig.Change{
		{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn", Instance: "canary"}, Key: "timeout_ms", Value: layerconfig.Value{Int: 750}},
	})
	show(s, layerconfig.Scope{Env: "prod", Region: "cn", Instance: "canary"}, "timeout_ms")

	// Rollback to version 1 creates a new version.
	v, err := s.Rollback(1)
	chk(err)
	fmt.Printf("rolled back to content of v1 as new version v%d\n", v)
	show(s, layerconfig.Scope{Env: "prod", Region: "cn"}, "features")
}

func pub(s *layerconfig.Store, cs []layerconfig.Change) {
	v, err := s.Publish(cs)
	chk(err)
	fmt.Printf("published -> version %d (%d changes)\n", v, len(cs))
}

func show(s *layerconfig.Store, sc layerconfig.Scope, key string) {
	r, err := s.Resolve(-1, sc, key)
	chk(err)
	if !r.Present {
		fmt.Printf("resolve %-12s at %+v = UNSET\n", key, sc)
		return
	}
	fmt.Printf("resolve %-12s at %+v = %+v\n", key, sc, r.Value)
}

func must(err error) { chk(err) }

func chk(err error) {
	if err != nil {
		panic(err)
	}
}
