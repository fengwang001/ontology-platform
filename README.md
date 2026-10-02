# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 对象存储生命周期计费

实现在 `lifecycle.go`，所有金额使用 `math/big.Int`，避免高单价、大对象和长周期相乘时溢出。

### 层级推导

对象不保存层级，只由最近访问日 `la` 与操作时间 `now` 推导，令 `Δ = now - la`：

- 热层：`Δ < A`。
- 凉层：`A <= Δ < B`，进入日为 `la + A`，凉层停留为 `Δ - A`。
- 冷层：`Δ >= B`，进入日为 `la + B`，冷层停留为 `Δ - B`。

### 存储费与早离补费

结清时存储费为：

```text
size × (
  p0 × min(Δ, A)
  + p1 × max(0, min(Δ, B) - A)
  + p2 × max(0, Δ - B)
)
```

早离补费只检查 `now` 所处的当前层：

- 热层补费为 `0`。
- 凉层补费为 `max(0, m1 - (Δ - A)) × p1 × size`。
- 冷层补费为 `max(0, m2 - (Δ - B)) × p2 × size`。

对象已经经过并转出的旧层不再补费；例如已经进入冷层后，只计算冷层未满最短停留的部分。

### 检索与免费额度

`Get` 先按读取发生时的层级结清存储费和早离补费，再计算检索费，最后把 `la` 重置为 `now`：

- 热层检索费为 `0`，也不切换或消耗免费额度。
- 凉层检索单价为 `r1`，冷层检索单价为 `r2`。
- 周期号为 `floor(now / 30)`。周期变化时，已用免费量清零。
- 免费量 `fg = min(size, max(0, Q - used))`，随后 `used += fg`。
- 检索费为 `(size - fg) × 当前层检索单价`。

免费额度由所有对象在同一计费器实例内按调用次序共享。`GetMany` 先确认所有键都存在，再按给定次序执行；重复键的后一次读取能看到前一次重置后的 `la`。

### 操作与拒绝顺序

- `Put`：新键建立对象，已有键先对旧对象结清，再用新大小和 `la=now` 覆盖。
- `Delete`：结清存储费和早离补费后删除对象。
- `Get`：结清并收取检索费后重置 `la`。
- `GetMany`：任一键不存在即整体拒绝，不改变对象、最大时钟和免费额度。

拒绝原因按固定优先级返回第一个错误：

1. 参数非法：键为空、键超过 64 字节、`size` 或 `now` 越界。
2. 时钟回退：`now` 小于已接受的最大 `now`。
3. 对象不存在：`Get`、`Delete` 或 `GetMany` 中第一个不存在的键。

被拒绝的操作不会改变任何对象、最大 `now`、周期号或已用免费量。所有公开操作都在互斥保护下完成，因此并发结果等价于某种合法串行顺序。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机序列的输入、输出、错误和朴素模拟判定依据
go test -v -run TestRandomSequencesMatchNaiveSimulation ./...

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
