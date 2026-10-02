# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Futex 等待队列子系统（`futex` 包）

带值校验、优先级队列、位掩码唤醒、重排队与复合唤醒的 futex 等待队列，
由注入时钟驱动；所有操作可并发调用，整体等价于某个串行顺序。

### 数据模型与队列次序

- 内存字以非负 `int64` 地址为键、值为 `int32`，缺省 `0`；`Store` 只改字，不唤醒。
- 每个地址一条等待队列，次序为 `(prio 升序, 同 prio 内入队先后)`；`prio` 取 0..99。
- 队列实现为按 prio 分组的双向链表，插入时追加到该 prio 组的组尾：
  新组通过向后扫描找到第一个更大 prio 的节点插入其前（最多扫过 100 个 prio 组），
  已有组借助组尾指针 O(1) 定位。
- 每次成功入队获得全局递增 `seq`（从 1 起）。
- 线程状态为 空闲/等待（带地址）/已唤醒/已超时/已中断，保持到下一次成功 `Wait`。

### 原子性与值校验

- `Wait` 的错误顺序固定为：参数非法 → 忙 → 值已变 → 立即超时 → 队列已满。
  值校验与入队是同一个原子步骤，因此不可能出现“读到旧值、入队前已被
  `Store`+`Wake` 抢先”而丢失唤醒。
- `Requeue` / `WakeOp` 的全部步骤各自也是单一原子步骤；被拒绝的操作不改变
  内存字、队列、线程状态、`seq` 与时钟。

### bitset 唤醒与重排队

- `Wake(addr, n, bitset)` 自队首扫描，仅唤醒 `bitset & waiter.bitset != 0`
  的等待者（至多 `n` 个，`n=0` 不唤醒）；不匹配者留在原位，返回序列等于
  它们离队前的队列次序。
- `Requeue(a1,a2,nWake,nRequeue,check,expected)`：参数非法（含 `a1==a2`）
  先于 `check` 的值已变；随后不看 bitset 唤醒 `nWake` 个，再把至多
  `nRequeue` 个按原次序移到 `a2`，逐个插入 `a2` 其 prio 组的组尾
  （不按原 `seq` 排序），保留 `bitset`/`deadline`，状态仍为等待。

### WakeOp 与超时

- `WakeOp` 先读 `a2` 旧值 `old`，再原子改写为 `op(old,arg)`
  （`SET/ADD/OR/ANDN/XOR`，`ADD` 按 `int32` 回绕，`ANDN = old &^ arg`），
  然后无条件唤醒 `a1` 前 `n1` 个；仅当 `cmp(old,cmparg)` 对**改写前旧值**
  成立（`EQ/NE/LT/LE/GT/GE`）时，再唤醒 `a2` 前 `n2` 个。比较不成立时
  内存字仍被改写。`a1==a2` 时第二段在已唤醒 `n1` 个之后的剩余队列上继续，不重复。
- `Advance(now)` 拒绝回退；推进后按 `(deadline 升序, seq 升序)` 返回并摘出
  超时者（`deadline == now` 即超时，`deadline == now+1` 不超时；`deadline=0`
  表示不超时）。超时集合用全局最小堆维护。
- `Cancel(tid)` 摘出等待者并置为已中断；负 tid 为参数非法，否则未在等待报错。

### 复杂度与 `examined` 计数器

- `Wake` 考察节点数 ≤ 实际唤醒数 + 被跳过的 bitset 不匹配数，与其他地址无关。
- `Requeue` 至多考察 `nWake+nRequeue` 个；`WakeOp` 的两段同理只看队首若干。
- `Advance` 借助最小堆，考察数 ≤ 本次超时个数 + 1。
- 非导出计数通过 `Examined()` / `ResetExamined()` 读取，键为
  `wake`、`requeue`、`wakeop1`、`wakeop2`、`advance`；
  `futex/examined_test.go` 在 10^5 个分散地址/不同 deadline 的等待者上验证上界。

### 本地验证

```bash
# 全部测试（含 2000 组随机轨迹对拍朴素模型、10^4 并发无丢失唤醒）
go test ./futex/ -v

# 打印每条随机轨迹的输入、输出与逐步状态判定
FUTEX_TRACE_LOG=1 go test ./futex/ -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./...
```

对拍模型见 `futex/naive_test.go`：每地址一个线性切片，插入时线性扫描
prio 组尾；随机测试逐步比较两侧返回值与完整状态快照（内存、各地址队列、
各线程状态/地址、时钟），并核对记账恒等式
`成功 Wait 数 == 已唤醒 + 已超时 + 已中断 + 仍在等待`。

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
