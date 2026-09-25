package check

import (
	"math/rand/v2"

	"ontology/rot"
)

type fixedCase struct {
	name   string
	nums   []int
	target int
	want   int
}

type errorCase struct {
	name string
	nums []int
	err  error
}

func LinearSearch(nums []int, target int) int {
	for index, value := range nums {
		if value == target {
			return index
		}
	}
	return -1
}

func newDeterministicRandom() *rand.Rand {
	return rand.New(rand.NewPCG(1, 2))
}

func rotatedCase(random *rand.Rand) ([]int, int) {
	nums := make([]int, random.IntN(30)+1)
	for index := range nums {
		nums[index] = 2 * index
	}
	pivot := random.IntN(len(nums) + 1)
	nums = append(append([]int{}, nums[pivot:]...), nums[:pivot]...)
	return nums, random.IntN(2*len(nums)+2) - 1
}

func concurrentSearchWorks() bool {
	done := make(chan bool, 32)
	for range 32 {
		go func() {
			done <- rot.Search([]int{4, 5, 6, 7, 0, 1, 2}, 0) == 4
		}()
	}
	for range 32 {
		if !<-done {
			return false
		}
	}
	return true
}
