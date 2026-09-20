# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 加权水塘抽样器

包 `ontology` 提供可复现的加权水塘抽样器（`Sampler`），
从长度未知的流中抽取固定容量 `k` 的样本，只依赖标准库，状态在进程内存。

### 算法

使用 **A-ExpJ**（Efraimidis & Spirakis, 2006,
*Weighted random sampling with a reservoir*）：
对每个权重为 `w` 的元素抽取 `u ~ Uniform(0,1]`，计算键 `key = u^(1/w)`，
水塘用最小堆始终保留 `key` 最大的 `k` 个元素。
每个元素只处理一遍，水塘之外不缓存任何流数据，
因此内存严格为 **O(k)**，与流长度无关；
可以证明任意元素留在水塘中的概率与其权重 `w` 成正比。

### 可复现性

全部随机性来自构造时显式传入的 `uint64` 种子（内部为 splitmix64），
不用全局 rand、不读时间、不依赖 map 迭代顺序。
随机性只发生在 `Add` 时（每接受一个元素恰好消耗 1 个随机数）；
`Sample` 是纯读操作，不消耗随机数、不改变内部状态，
无新增元素时多次 `Sample` 结果完全一致。

### 演示

```bash
go run ./cmd/demo
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
