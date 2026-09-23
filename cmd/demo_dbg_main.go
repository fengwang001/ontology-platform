package main
import ("fmt"; "ontology/chunk")
func main(){
  for _, s := range chunk.Split("a１２🙂9b") {
    fmt.Printf("%v %q\n", s.Kind, s.Text)
  }
}
