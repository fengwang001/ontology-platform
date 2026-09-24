package main
import ("fmt";"ontology/norm")
func main(){
 for _,s := range []string{"", "abc", "ab  \r\nc\t\nd", " \r\n", "\r\r\n  x \r\n","no newline   ", "\n\n\n", "a\rb", "x\r\n y\tz \n"} {
  for _,p := range []norm.Policy{norm.Keep,norm.EnsureOne,norm.TrimBlank}{
   out,m,_ := norm.Normalize([]byte(s),norm.Config{Policy:p})
   if len(out)>len(s) {
    fmt.Printf("%q pol %d -> %q origLen=%d outLen=%d runs=%d\n",s,p,out,m.OrigLen(),m.OutLen(),m.NumRuns())
   }
  }
 }
}
