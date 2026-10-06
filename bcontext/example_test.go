package bcontext_test

import (
	"bytes"
	"fmt"

	"ontology/bcontext"
)

// ExampleKernel 演示跨源隔离与能力求值，并展示日志会打印输入、输出与判定依据。
func ExampleKernel() {
	var buf bytes.Buffer
	k := bcontext.NewKernel(map[string]bcontext.Feature{
		"camera":   {Default: bcontext.DefaultSelf},
		"isolated": {RequiresIsolation: true, Default: bcontext.DefaultAll},
	}, bcontext.NewLogger(&buf))

	top, _ := k.NewTopLevel(bcontext.Document{
		Origin: "https://top.example", Opener: bcontext.OpenerSameOrigin,
		Embedder: bcontext.EmbedderRequireCorp,
		Permissions: map[string][]string{
			"camera":   {"https://top.example"},
			"isolated": {"*"},
		},
	})
	frame, _ := k.LoadFrame(top, bcontext.Document{
		Origin: "https://embed.example", Embedder: bcontext.EmbedderCredentialless,
	}, map[string][]string{"camera": {"https://embed.example"}, "isolated": {"*"}})

	iso, _ := k.Isolated(frame)
	camera, _ := k.Enabled(frame, "camera")
	gated, _ := k.Enabled(frame, "isolated")
	fmt.Printf("isolated=%v camera=%v gated=%v\n", iso, camera, gated)

	_, err := k.LoadFrame(top, bcontext.Document{
		Origin: "https://no-coep.example",
	}, nil)
	fmt.Printf("rejected=%v\n", err == bcontext.ErrEmbedderMismatch)
	fmt.Printf("logged=%v\n", bytes.Contains(buf.Bytes(), []byte("LoadFrame ok")))
	// Output:
	// isolated=true camera=true gated=true
	// rejected=true
	// logged=true
}
