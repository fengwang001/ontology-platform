# VMA 管理器

本包实现页粒度的进程地址空间 VMA（virtual memory area）管理器。地址、长度、偏移都以页为单位，所有区间使用半开区间 `[start, end)`。

## 数据模型

`VMA` 包含：

- `Start`、`End`：半开页区间，始终位于构造参数 `[low, high)` 内。
- `Perm`：0 到 7 的权限位。
- `GrowsDown`：是否为向下增长栈。
- `Anonymous` 与 `Source{File, Off}`：匿名映射，或文件号不小于 1 的文件映射起点页偏移。

相邻 VMA `a`、`b`（`a.End == b.Start`）只在以下条件全部满足时兼容并必须立即合并：

- `Perm` 相同；
- `GrowsDown` 相同；
- 二者都匿名；或文件号相同，且 `b.Off == a.Off + (a.End-a.Start)`。

区域表始终按 `Start` 升序、互不重叠、个数不超过 `MAXV`，且不存在相邻兼容却未合并的一对。

## 放置与保护区

每个向下增长 VMA `v` 的保护区为：

```text
[max(low, v.Start-G), v.Start)
```

非 `FIXED` 自动放置不能落入 VMA 或任何保护区。提示 `hint` 只有在非 0、范围合法且整个 `[hint,hint+length)` 都避开 VMA 与保护区时使用；否则自顶向下搜索：

1. 从可用页范围移除所有 VMA 与保护区；
2. 找所有长度至少为 `length` 的空闲区间；
3. 选择右端最大的区间，起点为其右端减 `length`；
4. 找不到则返回 `ErrNoSpace`。

`FIXED` 映射允许覆盖保护区。若与现有 VMA 相交：

- 带 `NOREPLACE` 时返回 `ErrExists`；
- 否则先按取消映射规则取消相交部分，再放置新 VMA。

放置后依次检查下邻与上邻，兼容则合并；若一个邻居也没有合并，才占用一个新的区域名额。

## 劈分与峰值校验

取消映射或改保护时，区间左端点严格落在某 VMA 内部会产生一次左劈分，右端点严格落在内部会产生一次右劈分；同一 VMA 可劈两次。

峰值校验按“当前区域数 `c` 加需要劈分次数 `s`”计算：

```text
c + s <= MAXV
```

校验发生在删除或修改之前。即使取消后最终区域数更少，或劈分后净增为 0，也不能抵扣、不能延后；失败返回 `ErrTooMany` 且不改变表。

`Mprotect` 要求目标区间被 VMA 完整覆盖，空洞返回 `ErrNoMem`。权限已经相同的 VMA 不劈分。完成后只在受影响区间两端各向外扩展一个相邻 VMA 的范围内反复合并兼容对。

## Grow

`Grow(addr)` 模拟向下增长栈在 `addr` 的缺页，按顺序检查：

1. `addr` 必须在 `[low, high)`，否则参数非法；
2. `addr` 未被 VMA 覆盖，否则 `ErrMapped`；
3. 起点大于 `addr` 的最低 VMA 必须带 `GrowsDown`，否则 `ErrSegv`；
4. `v.End-addr <= SM`，否则 `ErrStackLimit`；
5. `addr` 与其下方最近 VMA 的 `End`（无下邻则为 `low`）距离至少 `G`，否则 `ErrNoRoom`。

成功后仅把栈 VMA 的 `Start` 改为 `addr`，区域个数不变，也不做合并。

## 并发与原子性

`Manager` 使用 `sync.RWMutex`：

- `Find`、`VMAs`、`Count` 使用读锁；
- `Mmap`、`Munmap`、`Mprotect`、`Grow` 使用写锁。

每个写操作先在有序快照上完成校验、劈分、删除、权限修改和合并，再一次性提交。观察者不会看到劈分或合并到一半的中间表。被拒绝的操作不改变区域表或保护区索引。

## 增强结构

内部使用两棵平衡结构：

- VMA AVL：按 `Start` 保存 VMA，支持 `Find`、前驱、后继、插入和删除；
- 占用/保护 Treap：以懒覆盖计数维护页区间的零计数游程，VMA 与栈保护区都加 1，非 FIXED 放置搜索最大零页游程；
- Treap 每个节点维护零计数游程的前缀、后缀、最大值，可直接剪枝找到右端最大的足够大空闲区间。

树内有非导出计数器 `visited`：

- `Find` 访问节点数不超过 `2*ceil(log2(n+2))+3`；
- 自顶向下放置搜索不超过 `4*ceil(log2(n+2))+8`。

`vma_perf_test.go` 在 `n=100000` 下验证这两个边界；普通测试默认跳过，使用 `-slow` 启用。

## 错误

错误按操作规定的顺序返回：

- `Mmap`：参数非法、`ErrExists`、`ErrNoSpace`、`ErrTooMany`；
- `Munmap`：参数非法、`ErrTooMany`；
- `Mprotect`：参数非法、`ErrNoMem`、`ErrTooMany`；
- `Grow`：参数非法、`ErrMapped`、`ErrSegv`、`ErrStackLimit`、`ErrNoRoom`。

## 本地验证

```bash
GOCACHE=/tmp/ontology-go-cache go test ./...
GOCACHE=/tmp/ontology-go-cache go test -race ./...
GOCACHE=/tmp/ontology-go-cache go test ./... -slow -v
GOCACHE=/tmp/ontology-go-cache go vet ./...
/usr/local/go/bin/gofmt -w .
```

测试包括：

- 题目示例逐步复现；
- 最高空闲区间选择、同高取最高、hint 命中与因 VMA/保护区失败；
- 匿名、文件偏移连续、偏移差 1、`GrowsDown` 差异的合并规则；
- `FIXED`、`NOREPLACE`、完整覆盖删除和两侧劈分；
- `Munmap`、`Mprotect`、`FIXED Mmap` 三条路径的峰值恰好通过与差 1 失败；
- 空洞取消零副作用、`Mprotect` 空洞零副作用、相同权限不劈分和两端扩展合并；
- `Grow` 的参数、已映射、无栈、栈上限、保护间隙和成功路径；
- 2000 组小地址随机操作序列，与逐页数组、线性扫描的朴素模型逐项比较返回值、错误和最终 VMA 表。
