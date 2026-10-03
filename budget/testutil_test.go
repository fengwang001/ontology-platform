package budget

import "math/rand"

func randTasks(rng *rand.Rand, n int, maxT int64) []Task {
	tasks := make([]Task, 0, n)
	for i := 0; i < n; i++ {
		period := int64(1 + rng.Intn(int(maxT)))
		c := int64(1 + rng.Intn(int(period)))
		d := c + int64(rng.Intn(int(period-c+1)))
		tasks = append(tasks, Task{ID: string(rune('a' + i)), C: c, T: period, D: d})
	}
	return tasks
}
