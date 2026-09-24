// demo 演示组提交器的关键保证，逐项打印 OK/FAIL。
package main

import (
	"fmt"
	"os"
	"time"

	"ontology/batcher"
	"ontology/req"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fails++
			fmt.Println("FAIL " + name)
		}
	}

	check("triggers count/bytes/timeout", checkTriggers())
	fmt.Printf("TOTAL fails=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func checkTriggers() bool {
	b := batcher.New(3, 1<<20, time.Hour, nil)
	for i := 0; i < 3; i++ {
		b.Add(req.New([]byte{byte(i)}))
	}
	if !b.Full() || len(b.Drain()) != 3 {
		return false
	}
	b2 := batcher.New(100, 100, time.Hour, nil)
	b2.Add(req.New(make([]byte, 60)))
	if !b2.FlushFirst(50) {
		return false
	}
	b2.Drain()
	fired := make(chan time.Time, 1)
	b3 := batcher.New(100, 1<<20, time.Millisecond,
		func(time.Duration) <-chan time.Time { return fired })
	b3.Add(req.New([]byte{1}))
	fired <- time.Now()
	<-b3.Timer()
	return len(b3.Drain()) == 1
}
