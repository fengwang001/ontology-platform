# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Split Deque

分裂双端队列位于 `splitdeque` 包。元素类型为 `int64`，逻辑上按压入顺序使用单调递增的连续下标。内部用环形数组保存元素，但释放和回收只改变三个游标，不搬移数据。

构造方式：

```go
d, err := splitdeque.New(capacity, sharedLimit, privateReserve, freshness)
```

参数分别是容量 `Cap`、共享区上限 `Sm`、私有保留量 `Rv`、请求保鲜期 `F`。任一参数越界时返回 `splitdeque.ErrInvalidConfig`，且不创建队列。

### 三个区间

任意时刻都保持 `t <= s <= b`：

- `[0, t)`：位于活动前端之前的历史下标，不属于当前 `S` 或 `P`。
- `[t, s)`：共享区 `S`，窃取者只能从前端按 FIFO 取走。
- `[s, b)`：私有区 `P`，所有者只能从后端按 LIFO 弹出。
- `[b, ...)`：活动后端之后的下标；可能曾被私有弹出，也可能是下一次写入位置。

因此：

- `|S| = s - t`，并且始终满足 `|S| <= Sm`。
- `|P| = b - s`。
- 当前元素数为 `b - t`。
- 总量守恒：压入数量 = 弹出数量 + 窃取数量 + `b - t`。

### 请求、缺口与年龄

窃取请求由 `fl`、`ag`、`dm` 描述：

- `fl`：是否存在尚未满足的窃取请求。
- `ag`：请求年龄，每次释放检查不能释放元素时加一。
- `dm`：请求缺口，即最近一次未满足请求还缺少的元素数。

`Steal(m)` 取 `k = min(m, |S|)` 个共享区最旧元素。只有 `k < m` 时设置请求：`fl=true`、`ag=0`；第一次落空的缺口为 `m-k`，若请求已经存在，则 `dm=max(dm, m-k)`。恰好取满 `k=m` 不设置或修改请求。

释放检查只在 `fl=true` 时执行：

```text
r = min(
    max(floor(|P| / 2), dm),
    Sm - |S|,
    |P| - Rv,
)
```

负数按 0 处理。当 `r >= 1` 时执行 `s += r`，把私有区最旧的 `r` 个元素转为共享元素，同时清除 `fl`、`ag`、`dm`。否则执行 `ag += 1`；当 `ag == F` 时清除整个请求。

执行顺序：

- `Push(x)`：先在 `b` 处写入并执行 `b += 1`，再做释放检查；队列满时返回 `ErrFull`，不写入也不检查。
- `Pop()`：先做释放检查，再从私有区后端取元素。

### 回收

当 `Pop()` 时私有区为空但共享区非空，所有者回收：

```text
g = ceil(|S| / 2) = (|S| + 1) / 2
s -= g
```

这会把共享区最新的 `g` 个元素转回私有区，随后从 `b-1` 弹出。回收只移动分裂点 `s` 和取元素游标 `b`，不复制或搬移数组内容。

### 操作与统计

- `Push(x) error`：满员返回 `ErrFull`。
- `Pop() (int64, bool)`：空队列返回 `0, false`；这是正常返回，但如果存在请求仍会累计年龄。
- `Steal(m) ([]int64, error)`：`m` 不在 `[1, Cap]` 时返回 `ErrInvalidArgument`；`k=0` 时返回非错误的空切片。
- `Stats()`：返回压入、弹出、窃取元素个数，`k < m` 的窃取次数，释放次数和回收次数。

队列内部还有非导出的 `movedElements` 与 `copiedElements` 计数器。测试用它们证明释放和回收的数组元素搬移次数恒为 0，而 `Steal` 恰好复制 `k` 个元素。

所有方法共用一个互斥锁，并发的所有者操作与窃取者操作等价于某个合法串行顺序。

### 本地验证

```bash
# 全量测试
go test ./...

# 2000 组随机参数/操作序列与朴素模型逐步对照；-v 打印每组输入、输出和判定依据
go test -v ./splitdeque -run TestRandomAgainstNaiveModel

# 并发竞态检测
go test -race ./splitdeque -run TestConcurrentOperations

# 代码检查
gofmt -l .
go vet ./...
```

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
