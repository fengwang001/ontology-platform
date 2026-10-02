# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## `ontology/refheap`：软/弱/虚引用与终结化堆回收模型

`ontology/refheap` 是一个可精确复现存活、引用清除、入队次序、终结化复活与堆占用的
内存回收模型。对象编号从 1 起（普通对象与引用对象共用序列），引用队列号从 1 起；
全部操作在同一把锁下线性化，一次 `Collect` 对任何观察者都是原子步骤。

### 构造与操作

- `NewHeap(C, U, M)`：容量 `C`（1..1e12）、软引用单位 `U`（1..1e9）、每单位保留
  时长 `M`（0..1e9）。
- `Alloc(s, f, finalizable)`、`CreateQueue()`、`NewRef(kind, target, queue, s, now)`
  （软/弱/虚，目标与队列均可为 0）。
- `SetField(o,i,t)`、`SetRoot/ClearRoot`、`Get(r,now)`（仅软引用刷新时间戳）、
  `Poll(q)`、`Finalize(n)`、`Collect(now)`。

### Collect 的六个阶段

同一阶段内引用对象按编号升序处理；“入队”仅在引用对象带队列时按处理次序追加。

1. **强标记**：根集合与终结队列中尚未终结的对象为起点，仅沿普通对象字段传递；
   引用对象可被标记但不沿目标传递。`used0` = 此刻已标记对象大小之和。
2. **软引用**：候选为①结束时已被标记的软引用；`free = C - used0` 在本阶段开始时
   一次算定，不随本阶段保留目标而变小。目标非空且未标记时，当且仅当
   `now - 时间戳 <= ⌊free/U⌋ × M` 保留（沿字段继续标记）；否则清除目标并入队。
   边界相等时保留，大 1 时清除；`M=0` 时仅 `now == 时间戳` 保留。
3. **弱引用**：已标记弱引用的目标未标记即清除并入队——先于终结阶段，故被复活的
   目标读回仍为空。
4. **终结**：一次性选定所有“未标记、可终结标志为真、不在终结队列中”的普通对象
   （选定集合此刻固定，不受复活影响），按编号升序入 FIFO 终结队列，再把它们连同
   字段可达对象全部标记（复活）。因此被另一个当轮可终结对象引用的对象也会同轮入选。
5. **兜底**：此刻已标记的任何种类引用，若目标仍未标记，清除并入队（覆盖因④复活才
   可达的引用对象；虚引用通常在此阶段清除）。
6. **清扫**：回收全部未标记对象并减少已用量。`CollectResult` 返回回收编号（升序）、
   ②③⑤的清除/入队序列、④新选定的终结编号与回收后已用量。

终结队列中的对象在 `Finalize` 之前是强标记起点；`Finalize(n)` 按 FIFO 取出至多 n
个并清除其可终结标志。

### 拒绝次序

只报第一个失败：参数非法 → 对象或队列不存在 → 类别不符 → 时钟回退
（仅 `Collect`/`NewRef`/`Get` 携带 `now`，初值 0）→ 堆已满。被拒绝的操作不推进
任何对象编号、队列、时钟，也不改变已用量。

### 本地验证

```bash
# 规则用例 + 2000 组随机差分对照 + 终结链/题目示例
go test ./ontology/refheap -v

# 随机用例的输入/输出/判定日志（逐阶段与朴素集合模拟对照）
REFHEAP_VERBOSE=1 go test ./ontology/refheap -run TestRandomDifferential2000 -v

# 并发线性化压测与竞态检测
go test -race ./ontology/refheap -run TestConcurrentHammer -v

go vet ./...
gofmt -l .
```

测试要点（`ontology/refheap/*_test.go`）：引用不沿目标传递、不可达引用不处理不入队、
软阈值相等保留/大 1 清除、`M=0`、`free` 阶段开始时冻结、软保留使下游弱引用存活、
弱引用先于复活清除、终结一次性选定、复活对象的虚引用下轮入队、终结队列是根、
④带回的引用由⑤处理、阶段内编号升序与队列 FIFO、无队列只清除、`Get` 只刷新软引用、
容量恰好允许/超 1 拒绝、拒绝次序与“拒绝不改状态”，以及每次 Collect 的标记访问计数
（`Heap.LastMarkVisitCount`）等于最终存活对象数。朴素模拟器（`naive_*_test.go`，
用集合逐阶段重算可达性）对 2000 组随机堆与操作序列做全状态差分对照。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
