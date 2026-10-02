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

## 模式匹配检查器

核心实现在 `match` 包：

- `DefineType(name, constructors)` 登记不可变代数数据类型；构造子名在所有类型间唯一，构造子声明顺序同时是穷尽性和规范反例的构造子顺序。
- `NewSession(typeName)` 创建独立匹配会话，`AddArm(session, pattern, guarded)` 在末尾追加臂并返回每个顶层 `Or` 分支及整条臂的冗余判定。
- `Check(session)` 返回是否穷尽；不穷尽时返回规范反例和可精确复现的文本。零字段构造子渲染为 `Name`，带字段渲染为 `Name(a, b)`，通配渲染为 `_`。

### 覆盖与冗余

一条未守卫臂的顶层分支是冗余的，当且仅当它能匹配的所有有限值都已经被：

- 此前所有未守卫臂；以及
- 同一条臂中更靠前的顶层分支

覆盖。守卫臂不加入后续臂和 `Check` 的覆盖集合；但判断本臂稍后分支时，同臂更前分支仍参与覆盖。整条臂冗余当且仅当所有顶层分支都冗余。任意嵌套位置的 `Or` 都按从左到右先序展开为行，只有最外层 `Or` 会分别报告分支冗余。

### 规范反例

`Check` 收集所有未守卫臂的展开行，调用矩阵函数 `Missing(rows, types)`：

1. 类型序列为空：行集为空则得到空反例，否则无反例。
2. 首列构造子集合 `Σ` 包含当前类型全部构造子：按声明顺序逐个特化构造子；通配行替换成字段数量个通配，构造子行保留同名列，递归取第一个仍有反例的构造子。
3. `Σ` 不完整：只保留首列为通配的行并去掉首列递归。`Σ` 为空时反例首项为 `_`；否则取声明顺序中第一个缺失构造子，并用字段数量个 `_` 补齐参数。

因此选择依据是声明顺序，不是字典序；冗余未守卫臂不改变覆盖，但仍属于 `Missing` 的输入行集。

### 缓存与并发

每次 `Check` 后缓存结果。之后只加入守卫臂或整条冗余的未守卫臂时，再次 `Check` 直接返回缓存；加入任何含非冗余分支的未守卫臂会使缓存失效。`MissingCalls()` 返回非导出规范递归的累计调用次数，可用于验证缓存命中。所有公开操作共用互斥锁，并发调用等价于某个合法串行顺序。

### 本地验证

```bash
go test ./...
go test -race ./match
go test ./match -run TestRandomOracleComparison -count=1 -v
```

随机测试固定种子重放 2000 组类型和臂序列，使用深度不超过模式最大深度加 2 的朴素有限值枚举，对照分支冗余、整体穷尽性、反例未覆盖性，并在日志中打印输入、输出和判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
