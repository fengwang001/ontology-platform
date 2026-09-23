package main

import (
	"fmt"
	"reflect"
	"unsafe"

	"ontology/audit"
	"ontology/freelist"
	"ontology/handle"
	"ontology/slot"
	"ontology/table"
)

func main() {
	checks := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
			checks++
		} else {
			fmt.Println("FAIL", name)
		}
	}

	entry := slot.New[string]()
	entry.Allocate("v")
	check("slot lifecycle", entry.Live() && entry.Value() == "v" && entry.Release(3) && entry.Free())
	first := handle.Encode(1, 0, 1)
	_, slotIndex, generation := first.Decode()
	check("handle encode/zero", !first.IsZero() && slotIndex == 0 && generation == 1 &&
		handle.Handle{}.IsZero() && !first.Equal(handle.Encode(2, 0, 1)))
	slots := []*slot.Slot[string]{slot.New[string](), slot.New[string](), slot.New[string]()}
	free := freelist.New(slots)
	firstSlot, _ := free.Pop()
	free.PushTail(firstSlot)
	secondSlot, _ := free.Pop()
	check("freelist FIFO/no-scan", firstSlot == 0 && secondSlot == 1 &&
		readPrivate(free, "visited") == 1)
	tab, _ := table.New[string](4)
	handleA, _ := tab.Insert("A")
	valueA, getErr := tab.Get(handleA)
	removeErr := tab.Remove(handleA)
	_, staleErr := tab.Get(handleA)
	check("table get/remove/stale", valueA == "A" && getErr == nil && removeErr == nil &&
		staleErr == table.ErrStaleHandle)
	check("audit partition", audit.Check(tab, nil) == nil)
	_ = checks
}

func readPrivate(value any, name string) uint64 {
	field := reflect.ValueOf(value).Elem().FieldByName(name)
	return uint64(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}
