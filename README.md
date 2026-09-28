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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 加权不放回抽样器（`sampler` 包）

`sampler` 实现了从带权元素流中按权重抽取给定个数样本的功能，算法为
Efraimidis–Spirakis 加权蓄水池抽样（A-Res），流式结果与一次性批量计算严格一致。

### 键值计算

- 每个**提交成功**（通过全部输入校验）的元素从调用方注入的随机源取一个
  `u ∈ (0,1)`，计算键值 `key = u^(1/weight)`。
- 权重越大，键值分布越偏向 1，被抽中的概率越高。
- 随机数由调用方以确定序列注入（`sampler.NewSequence` 或自定义 `Source`），
  因此结果完全可复现。

### 排名与替换规则

- 样本集合容量为 `sampleSize`，保留键值最大的若干元素。
- 排名比较：先比键值，**键值相等时先到达者排名更高**。
- 样本未满：新元素直接进入样本（判定 `ACCEPT`）。
- 样本已满：与当前排名最低者（小顶堆堆顶）比较；仅当新元素排名**严格更高**
  时才替换堆顶（判定 `REPLACE`），否则该元素出局（判定 `OUT`）。
- 上述流式规则等价于：对全部提交成功的元素统一计算键值后排序取前
  `sampleSize` 个（见测试中的批量排序参照）。

### 随机数守恒

- 每个提交成功的元素恰好消耗一个随机数；抽样器消耗计数与随机源游标始终一致，
  可用 `Sampler.Check()` 自检。
- 提交被**拒绝**（返回错误，原因见下）时不消耗任何随机数，随机源游标不动，
  样本、已见标识集合与计数器保持调用前状态——失败不留痕。
- 随机数用尽后再次取数返回 `ErrRandomsExhausted`；随机源给出 `(0,1)` 之外的
  值返回 `ErrIllegalRandom`，且该值保留在游标处不会被跳过。
- 提交成功但未能留在最终样本中的元素属于正常“出局”，其随机数与批量算法一样
  照常计入消耗。

### 边界与错误类别

七类失败各自对应互不相同、可用 `errors.Is` 精确区分的哨兵错误：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidSampleSize` | 样本数 `sampleSize <= 0` |
| `ErrEmptyID` | 元素标识为空字符串 |
| `ErrDuplicateID` | 元素标识此前已提交成功 |
| `ErrZeroWeight` | 权重为 `0` |
| `ErrIllegalWeight` | 权重为负数、`NaN` 或无穷大 |
| `ErrIllegalRandom` | 随机数不在 `(0,1)` 内，或构造时随机源为 `nil` |
| `ErrRandomsExhausted` | 随机源已无数可取 |

其他说明：

- `Submit`、`Samples`、`Consumed`、`Size`、`Check` 均为并发安全；同一随机源
  游标下每个随机数恰好被一个成功提交消耗（提交在互斥区内完成“取数 + 入堆”）。
- `Samples()` 返回按键值降序（并列先到达者在前）的快照拷贝。
- 可通过 `WithLogger` 注入日志器；日志逐行打印每步的标识、权重、随机数、键值
  与判定依据（`ACCEPT` / `REPLACE` / `OUT` / `reject` 及原因）。

### 用法示例

```go
rng := sampler.NewSequence([]float64{0.21, 0.77, 0.04})
s, err := sampler.New(2, rng /*, sampler.WithLogger(log) */)
// ...
accepted, err := s.Submit("item-1", 2.5)
result := s.Samples()    // 按排名排序的样本
used := s.Consumed()     // 已消耗随机数个数
if err := s.Check(); err != nil { ... }
```

### 本地验证

```bash
# 全量测试（含竞态检测），步骤日志随 -v 打印
go test -race -v ./sampler/

# 全部包 + 覆盖率
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```
