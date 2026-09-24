package props

import (
	"fmt"
	"strings"
	"testing"
)

func TestDbg(t *testing.T) {
	q, err := Load(strings.NewReader("\\f#\\:=\\\\\n"))
	fmt.Printf("pairs=%#v err=%v\n", q.Pairs(), err)
}
