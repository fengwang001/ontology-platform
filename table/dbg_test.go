package table_test

import ("testing"; "fmt"; "ontology/table")
func TestDbg(t *testing.T){
  in:="a,b\n1,2\n3,4"
  p:=table.NewParser(table.Limits{})
  fmt.Println("feed err:",p.Feed([]byte(in)))
  tb,e:=p.Close(); fmt.Println("close",e)
  if tb!=nil { for _,r:=range tb.Records(){ for _,c:=range r{ fmt.Printf("%q ",c.Value)}; fmt.Println()}}
}
