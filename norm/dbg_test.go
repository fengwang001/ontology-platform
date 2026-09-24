package norm

import (
	"fmt"
	"testing"
)

func TestDbg(t *testing.T) {
	n := New(Config{})
	n.Write([]byte("a  \r\n"))
	fmt.Printf("A out=%q ol=%d pl=%d segs=%+v\n", n.Output(), n.Mapper().OrigLen(), n.Mapper().OutLen(), n.Mapper().Debug())
	n.Write([]byte("b\t\r\n"))
	n.Close()
	fmt.Printf("out=%q ol=%d pl=%d segs=%+v\n", n.Output(), n.Mapper().OrigLen(), n.Mapper().OutLen(), n.Mapper().Debug())
	for o := 0; o <= n.Mapper().OutLen(); o++ {
		i := n.ToOrig(o)
		fmt.Printf("o=%d i=%d back=%d\n", o, i, n.ToOut(i))
	}
}
