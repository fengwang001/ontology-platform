package stream
import ("testing";"fmt")
func TestDbg2(t *testing.T){
 tr:=New(Config{Dir: U8ToU8})
 for _,b := range []byte{0xF0,0x90,0x80} {
  tr.feed8(b)
  fmt.Printf("after %02x pendlen=%d want=%d out=%x\n", b, len(tr.pend), LeadLenTest(0xF0), tr.out)
 }
}
func LeadLenTest(b byte) int { return 4 }
