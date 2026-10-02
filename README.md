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

## 按函数登记的逃逸分析摘要

`NewRegistry()` 创建并发安全的函数注册表。`Register(name, k, V, statements)` 按登记顺序分配全局分配点编号；`Summary(name)` 返回摘要和分析轮数，`Sites(name)` 返回该函数内按编号升序排列的分配点分类。查询返回副本，调用方修改结果不会影响注册表。

### 指向约束

分析是字段不敏感、流不敏感的最小不动点分析，反复扫描全部语句直到集合不再变化。对象包括：

- 参数对象 `P0..P(k-1)`：变量 `0..k-1` 的初始指向。
- 分配点对象：每条 `New(d)` 对应一个对象。
- 调用结果对象 `X`：每个接收结果（`d != -1`）且被调函数 `fresh=true` 的 `Call` 各有一个对象，并从分析开始即带全局标记。

变量指向集 `pts(i)` 和对象指向集 `heap(o)` 只做并集：

- `New(d)`：`pts(d) += 分配点对象`。
- `Copy(d,s)`：`pts(d) += pts(s)`。
- `Store(d,s)`：对 `pts(d)` 中每个 `o`，`heap(o) += pts(s)`。
- `Load(d,s)`：对 `pts(s)` 中每个 `o`，`pts(d) += heap(o)`。
- `Ret(s)`：返回集 `ret += pts(s)`。
- `Global(s)`：给 `pts(s)` 中所有对象种全局标记。
- `Call(d,g,args)` 使用 `g` 的摘要传播 `glob`、`ret` 和 `E`；`fresh` 时向接收变量加入本条调用的 `X`。`d=-1` 不创建接收结果，也不产生返回对象或 `X` 的传播。

因此，对没有任何 `Store` 写入的参数对象执行 `Load` 会得到空集；调换非分配语句的次序不会改变结果。

### 标记与分类

- `GLB`：全局标记种子（含初始 `X`）沿 `heap` 边的可达闭包，包含种子自身。
- `RR`：`ret` 中对象沿 `heap` 边的可达闭包，包含种子自身。
- `PR`：参数对象沿 `heap` 边至少走一步可达的对象，不包含参数自身。

每个分配点按优先级分类为：`GlobalEscape`（在 GLB）、`ReturnEscape`（否则在 RR）、`ParameterEscape`（否则在 PR）、`StackEscape`（其他）。

### 摘要与自递归

被调函数摘要：

- `Ret[i]`：`Pi` 位于 `RR`。
- `Glob[i]`：`Pi` 位于 `GLB`。
- `E[i][j]`：`Pj` 是 `heap(Pi)` 的直接元素。
- `Fresh`：`ret` 直接包含某个分配点对象或调用结果对象 `X`。

自递归调用第一轮使用全假摘要；每轮完整重做分析并生成新摘要。所有摘要位只会从假变真，直到新摘要与本轮使用的摘要相同。不含自调用的函数恰为 1 轮；含自调用的函数轮数有上界 `2k+k²+2`（2 个 `ret/glob` 位、`k²` 个 `E` 位、`fresh` 位，再加确认轮）。

### 本地验证

标准验证：

```bash
go test -race -v ./...
```

随机朴素对照会运行 2000 组合法函数序列，日志逐条打印输入、实际输出、朴素模拟器输出和判定依据；朴素模拟器独立反复扫描全部约束，再计算 `GLB/RR/PR`：

```bash
go test -run TestRandomNaiveOracle2000 -v ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
