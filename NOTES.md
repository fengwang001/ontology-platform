# NOTES

## 满/空判定方案推导（第三节）

环形缓冲区用读指针 r、写指针 w 对 cap 取模循环复用固定数组。
空时 r == w；持续写入后 w 追上 r，满时也有 r == w。指针重合同时对应
「空」与「满」两种状态，仅靠两个指针无法区分，这正是线上事故的根因：
满缓冲被误判为空，新数据覆盖了未消费的数据。

常见解法有两种：

1. 牺牲一个槽位：以 `(w+1)%cap == r` 判满。此时最多只能存 cap-1 个
   元素。对 `New(4)`，第 4 次 Enqueue 时 w=3、`(3+1)%4==0==r`，
   即被判为满而拒绝——只能装 3 个，违反「连续 Enqueue 成功 4 次」。
2. 额外维护 size 计数器：空 = `size==0`，满 = `size==cap`，指针重合
   时由计数器仲裁。最多可存 cap 个，满足语义。

题目第二节第 1 条要求 `New(4)` 连续 Enqueue 4 次成功、第 5 次才
ErrFull，因此必须选方案 2（size 计数器）。方案 1 会让第 4 次就报满，
由测试 `TestWastedSlotRejectsFourth` 内联该错误实现钉死这一结论。

## 语义落点（第二节）

1. 容量语义：`ring/ring.go` 的 `Enqueue`；`TestCapacityAndErrors`
2. FIFO 保序：`check/check.go` 的 `Ref` 参照；`TestFIFOVsRef`
3. 空/满区分：`ring.go` 的 size 计数器与 `Empty/Full`；`TestFIFOVsRef`
4. 哨兵错误：`ring.go` 的 `ErrBadCap/ErrFull`，`seq/seq.go` 复用；`TestCapacityAndErrors`
5. 资源上界：`ring.go` 的 max 字段与 `MaxLen`；`TestRandomOps`
6. 并发：`ring.go` 的 `sync.Mutex`；`TestConcurrent`（SPSC 与 MPSC）
