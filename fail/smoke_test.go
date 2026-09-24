package fail

import (
 "context"
 "errors"
 "fmt"
 "testing"
 "time"

 "ontology/exec"
 "ontology/graph"
)

func mk() (*graph.Graph, map[string]exec.TaskFunc) {
 g := graph.New()
 for _, n := range []string{"A","B","C","S"} { g.AddNode(n) }
 g.AddEdge("A","B"); g.AddEdge("B","C")
 fns := map[string]exec.TaskFunc{
  "A": func(context.Context) error { return errors.New("boom") },
  "B": func(context.Context) error { return nil },
  "C": func(context.Context) error { return nil },
  "S": func(ctx context.Context) error { select{case <-ctx.Done():return ctx.Err();case <-time.After(50*time.Millisecond):return nil} },
 }
 return g, fns
}

func TestSmokeFF(t *testing.T){
 g,fns:=mk()
 r,err:=Run(context.Background(),g,fns,Options{Limit:8,Mode:FailFast})
 if err!=nil{t.Fatal(err)}
 fmt.Println("\n"+r.Render())
}
func TestSmokeBE(t *testing.T){
 g,fns:=mk()
 r,err:=Run(context.Background(),g,fns,Options{Limit:8,Mode:BestEffort})
 if err!=nil{t.Fatal(err)}
 fmt.Println(r.Render())
 _ = time.Millisecond
}
