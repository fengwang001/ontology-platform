package ontology

import (
	"container/heap"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naiveCopy struct {
	src int64
	dst int64
	len int64
}

func TestRandomAgainstNaiveAndBuffer(t *testing.T) {
	rng := rand.New(rand.NewSource(1255))
	for iteration := 0; iteration < 2000; iteration++ {
		n := int64(rng.Intn(36) + 1)
		maxSize := int64(rng.Intn(48) + int(n))
		maxInstr := int64(rng.Intn(18) + 1)
		instructionCount := rng.Intn(int(maxInstr) + 1)
		instructions := make([]Instruction, 0, instructionCount)
		m := int64(0)

		for j := 0; j < instructionCount; j++ {
			if n > 0 && (j == 0 || rng.Intn(3) != 0) {
				length := int64(rng.Intn(int(n)) + 1)
				src := int64(rng.Intn(int(n - length + 1)))
				instructions = append(instructions, Instruction{Copy: &Copy{Src: src, Len: length}})
				m += length
			} else {
				length := int64(rng.Intn(8) + 1)
				data := make([]byte, length)
				for p := range data {
					data[p] = byte(rng.Intn(256))
				}
				instructions = append(instructions, Instruction{Add: &Add{Data: data}})
				m += length
			}
		}

		sorter := NewSorter(maxSize, maxInstr)
		got, err := sorter.Plan(n, instructions)
		if m > maxSize {
			if err != ErrSize {
				t.Fatalf("case %d: want ErrSize, got %v", iteration, err)
			}
			t.Logf("case=%d input={n:%d maxSize:%d maxInstr:%d delta:%s} output={error:ErrSize} basis={first-validation-error-equal:true}",
				iteration, n, maxSize, maxInstr, formatInstructions(instructions))
			continue
		}
		if err != nil {
			t.Fatalf("case %d: unexpected error %v", iteration, err)
		}
		want := naivePlan(n, instructions)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d plan mismatch\ngot:  %#v\nwant: %#v\ninput: n=%d maxSize=%d delta=%#v",
				iteration, got, want, n, maxSize, instructions)
		}
		assertExecutes(t, n, instructions, got, expectedFile(n, instructions))

		t.Logf("case=%d input={n:%d maxSize:%d maxInstr:%d delta:%s} output=%#v basis={naive-plan-equal:true buffer-execution-equal:true}",
			iteration, n, maxSize, maxInstr, formatInstructions(instructions), got)
	}
}

func formatInstructions(instructions []Instruction) string {
	var text []byte
	text = append(text, '[')
	for i, instruction := range instructions {
		if i > 0 {
			text = append(text, ' ')
		}
		if instruction.Copy != nil {
			text = append(text, fmt.Sprintf("Copy(src=%d,len=%d)", instruction.Copy.Src, instruction.Copy.Len)...)
		} else {
			text = append(text, fmt.Sprintf("Add(data=%q)", instruction.Add.Data)...)
		}
	}
	text = append(text, ']')
	return string(text)
}

func TestIntervalComparisonsAreSubquadratic(t *testing.T) {
	const k = 2000
	n := int64(k * 100)
	sorter := NewSorter(n, k)
	instructions := make([]Instruction, k)
	for i := range instructions {
		instructions[i] = Instruction{Copy: &Copy{Src: int64(i * 100), Len: 100}}
	}
	plan, err := sorter.Plan(n, instructions)
	if err != nil {
		t.Fatal(err)
	}
	sorter.mu.Lock()
	comparisons := sorter.comparisons
	sorter.mu.Unlock()

	limit := int64(k * k / 4)
	if comparisons >= limit {
		t.Fatalf("comparisons=%d, want far below %d", comparisons, limit)
	}
	if plan.Edges != 0 {
		t.Fatalf("identity copies should be discarded, edges=%d", plan.Edges)
	}
}

func naivePlan(n int64, instructions []Instruction) PlanResult {
	var copies []naiveCopy
	var adds []Op
	m := int64(0)
	for _, instruction := range instructions {
		if instruction.Copy != nil {
			dst := m
			m += instruction.Copy.Len
			if instruction.Copy.Src != dst {
				copies = append(copies, naiveCopy{
					src: instruction.Copy.Src,
					dst: dst,
					len: instruction.Copy.Len,
				})
			}
		} else {
			data := append([]byte(nil), instruction.Add.Data...)
			adds = append(adds, Op{Kind: OpAdd, Dst: m, Data: data})
			m += int64(len(data))
		}
	}

	active := make([]bool, len(copies))
	for i := range copies {
		active[i] = true
	}
	indegree := make([]int, len(copies))
	edgeCount := int64(0)
	for a := range copies {
		for b := range copies {
			if a != b && intervalsOverlap(copies[a].src, copies[a].len, copies[b].dst, copies[b].len) {
				indegree[b]++
				edgeCount++
			}
		}
	}

	ready := &dstHeap{}
	for i, degree := range indegree {
		if degree == 0 {
			*ready = append(*ready, i)
		}
	}
	heap.Init(ready)

	var order []int
	var stashed []naiveCopy
	for len(order)+len(stashed) < len(copies) {
		if ready.Len() > 0 {
			from := heap.Pop(ready).(int)
			if !active[from] || indegree[from] != 0 {
				continue
			}
			active[from] = false
			order = append(order, from)
			for to := range copies {
				if active[to] && intervalsOverlap(copies[from].src, copies[from].len, copies[to].dst, copies[to].len) {
					indegree[to]--
					if indegree[to] == 0 {
						heap.Push(ready, to)
					}
				}
			}
			continue
		}

		target := naiveChooseStash(copies, active)
		active[target] = false
		stashed = append(stashed, copies[target])
		for to := range copies {
			if active[to] && intervalsOverlap(copies[target].src, copies[target].len, copies[to].dst, copies[to].len) {
				indegree[to]--
				if indegree[to] == 0 {
					heap.Push(ready, to)
				}
			}
		}
		for from := range copies {
			if active[from] && intervalsOverlap(copies[from].src, copies[from].len, copies[target].dst, copies[target].len) {
				indegree[target]--
			}
		}
	}

	sortNaiveCopies(stashed)
	sortOpsByDst(adds)
	result := PlanResult{Ops: make([]Op, 0, len(stashed)*2+len(order)+len(adds)), Edges: edgeCount}
	for slot, copyOp := range stashed {
		result.Ops = append(result.Ops, Op{Kind: OpStash, Src: copyOp.src, Len: copyOp.len, Slot: slot})
	}
	for _, index := range order {
		result.Ops = append(result.Ops, Op{Kind: OpCopy, Src: copies[index].src, Dst: copies[index].dst, Len: copies[index].len})
	}
	for slot, copyOp := range stashed {
		result.Ops = append(result.Ops, Op{Kind: OpUnstash, Dst: copyOp.dst, Len: copyOp.len, Slot: slot})
	}
	result.Ops = append(result.Ops, adds...)
	for _, copyOp := range stashed {
		result.StashBytes += copyOp.len
	}
	result.StashedCopyCount = len(stashed)
	return result
}

func naiveChooseStash(copies []naiveCopy, active []bool) int {
	assigned := make([]int, len(copies))
	for i := range assigned {
		assigned[i] = -1
	}
	adj := make([][]int, len(copies))
	rev := make([][]int, len(copies))
	for a := range copies {
		if !active[a] {
			continue
		}
		for b := range copies {
			if a != b && active[b] && intervalsOverlap(copies[a].src, copies[a].len, copies[b].dst, copies[b].len) {
				adj[a] = append(adj[a], b)
				rev[b] = append(rev[b], a)
			}
		}
	}

	state := make([]uint8, len(copies))
	var finish []int
	for start := range copies {
		if !active[start] || state[start] != 0 {
			continue
		}
		stack := []int{start}
		state[start] = 1
		for len(stack) > 0 {
			from := stack[len(stack)-1]
			advanced := false
			for _, to := range adj[from] {
				if state[to] == 0 {
					state[to] = 1
					stack = append(stack, to)
					advanced = true
					break
				}
			}
			if !advanced {
				state[from] = 2
				finish = append(finish, from)
				stack = stack[:len(stack)-1]
			}
		}
	}

	componentID := 0
	componentSize := []int{}
	for i := len(finish) - 1; i >= 0; i-- {
		start := finish[i]
		if assigned[start] != -1 {
			continue
		}
		stack := []int{start}
		assigned[start] = componentID
		size := 0
		for len(stack) > 0 {
			from := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			for _, to := range rev[from] {
				if assigned[to] == -1 {
					assigned[to] = componentID
					stack = append(stack, to)
				}
			}
		}
		componentSize = append(componentSize, size)
		componentID++
	}

	target := -1
	for id := range copies {
		if active[id] && componentSize[assigned[id]] >= 2 {
			if target == -1 || copies[id].len < copies[target].len ||
				(copies[id].len == copies[target].len && copies[id].dst < copies[target].dst) {
				target = id
			}
		}
	}
	if target == -1 {
		panic(fmt.Sprintf("active graph has no cyclic SCC: %v", active))
	}
	return target
}

func intervalsOverlap(startA, lenA, startB, lenB int64) bool {
	return startA < startB+lenB && startB < startA+lenA
}

func sortNaiveCopies(copies []naiveCopy) {
	for i := 1; i < len(copies); i++ {
		for j := i; j > 0 && copies[j-1].dst > copies[j].dst; j-- {
			copies[j-1], copies[j] = copies[j], copies[j-1]
		}
	}
}

func sortOpsByDst(ops []Op) {
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && ops[j-1].Dst > ops[j].Dst; j-- {
			ops[j-1], ops[j] = ops[j], ops[j-1]
		}
	}
}
