// demo 构建一个微型仓库快照并执行求解，打印输入、输出与每一步判定依据。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"ontology"
)

type stdLogger struct{}

func (stdLogger) Logf(format string, args ...any) {
	log.Printf(format, args...)
}

func publish(r *depsolver.Repository, name, version string, deps map[string]string) {
	if err := r.Publish(depsolver.PackageVersion{Name: name, Version: version, Dependencies: deps}); err != nil {
		fmt.Fprintf(os.Stderr, "publish %s@%s failed: %v\n", name, version, err)
		os.Exit(1)
	}
}

func main() {
	r := depsolver.NewRepository()

	publish(r, "c", "1.0.0", nil)
	publish(r, "c", "2.0.0", nil)
	publish(r, "a", "1.0.0", map[string]string{"c": "~2.0.0"})
	publish(r, "a", "2.0.0", map[string]string{"c": "~1.0.0"})
	publish(r, "b", "1.0.0", map[string]string{"c": "~1.0.0"})
	publish(r, "b", "2.0.0", map[string]string{"c": "~2.0.0"})

	root := map[string]string{"a": ">=1.0.0 <3.0.0", "b": ">=1.0.0 <3.0.0"}
	fmt.Printf("INPUT  root=%v\n", root)

	sol, err := r.Solve(depsolver.WithLogger(context.Background(), stdLogger{}), root)
	if err != nil {
		log.Fatalf("solve failed: %v", err)
	}
	fmt.Printf("OUTPUT %v\n", sol)
}
