package hd

// 本文件实现按开始时刻排序的治疗索引。
//
// 关键取舍：每个机位 / 每位患者各自维护一棵以治疗开始时刻为键的确定性 treap。
// 判定一次治疗是否可行，只需取其开始时刻的前驱与后继各一次：由于同一机位上
// 已提交的治疗两两不重叠，前驱也是结束时刻最晚的治疗；后继同理。因此单次可行性
// 判定的比较次数与该机位历史治疗总数无关（treap 的期望深度由哈希优先级决定，
// 不随插入数量增长），只与当前机位数及该机位附近时段内的治疗数相关。
// 被放弃的方案：
// - 全量有序切片：插入/删除为 O(历史数)，违反复杂度约束；
// - 扫描全量历史取最大 end：开销随历史线性增长；
// - 区间桶 + 前驱链：删除与桶边界处理复杂且边界情形难复现。
// treap 使用确定性 splitmix64 优先级，保证相同操作序列在任何机器上结构一致，
// 从而分配结果可精确重放。

func hash64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

type node struct {
	t        *Treatment
	priority uint64
	left     *node
	right    *node
}

// treap 是以开始时刻为键的有序集合；同一键至多存在一条记录。
type treap struct {
	root *node
}

func (tr *treap) insert(t *Treatment) {
	pr := hash64(uint64(t.Start)*0x9e3779b97f4a7c15 + 0x243f6a8885a308d3)
	tr.root = insertNode(tr.root, &node{t: t, priority: pr}, t.Start)
}

func insertNode(root, n *node, key int) *node {
	if root == nil {
		return n
	}
	if key < root.t.Start {
		root.left = insertNode(root.left, n, key)
		if root.left.priority > root.priority {
			root = rotateRight(root)
		}
	} else {
		root.right = insertNode(root.right, n, key)
		if root.right.priority > root.priority {
			root = rotateLeft(root)
		}
	}
	return root
}

func rotateRight(r *node) *node {
	x := r.left
	r.left = x.right
	x.right = r
	return x
}

func rotateLeft(r *node) *node {
	x := r.right
	r.right = x.left
	x.left = r
	return x
}

// remove 按键删除节点。取消的治疗从索引中摘除，消毒占用随之消失。
func (tr *treap) remove(key int) {
	tr.root = removeNode(tr.root, key)
}

func removeNode(root *node, key int) *node {
	if root == nil {
		return nil
	}
	if key < root.t.Start {
		root.left = removeNode(root.left, key)
	} else if key > root.t.Start {
		root.right = removeNode(root.right, key)
	} else {
		switch {
		case root.left == nil:
			return root.right
		case root.right == nil:
			return root.left
		default:
			if root.left.priority > root.right.priority {
				root = rotateRight(root)
				root.right = removeNode(root.right, key)
			} else {
				root = rotateLeft(root)
				root.left = removeNode(root.left, key)
			}
		}
	}
	return root
}

// neighbors 返回严格小于 key 的最大键记录与严格大于 key 的最小键记录。
func (tr *treap) neighbors(key int) (pred, succ *Treatment) {
	cur := tr.root
	for cur != nil {
		if key < cur.t.Start {
			succ = cur.t
			cur = cur.left
		} else if key > cur.t.Start {
			pred = cur.t
			cur = cur.right
		} else {
			// 键已存在：调用方负责判冲突，这里分别再找两侧最近记录。
			if l := cur.left; l != nil {
				for l.right != nil {
					l = l.right
				}
				pred = l.t
			}
			if r := cur.right; r != nil {
				for r.left != nil {
					r = r.left
				}
				succ = r.t
			}
			return
		}
	}
	return
}

// baySchedule 为单个机位的治疗索引。
type baySchedule struct {
	committed treap
}

// patientSchedule 为单个患者跨机位的治疗索引。
type patientSchedule struct {
	committed treap
}
