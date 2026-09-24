package main

import (
	"fmt"

	"ontology/heap2"
)

func main() {
	minHeap := heap2.NewMin()
	minHeap.Push(2)
	minHeap.Push(1)
	min, _ := minHeap.Peek()
	maxHeap := heap2.NewMax()
	maxHeap.Push(1)
	maxHeap.Push(2)
	max, _ := maxHeap.Peek()
	status("heap2 min/max", min == 1 && max == 2 && minHeap.Len() == 2 && maxHeap.Len() == 2)
	fmt.Println("FAIL: median not implemented")
	fmt.Println("FAIL: api not implemented")
}

func status(name string, ok bool) {
	if ok {
		fmt.Println("OK:", name)
	} else {
		fmt.Println("FAIL:", name)
	}
}
