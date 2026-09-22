# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 在线统计量累加器

根包 `ontology` 提供并发安全的流式统计量累加器（`Accumulator`），
支持计数、均值、总体方差、样本方差与可交换的并行合并（`Merge`）。

### 递推公式

单样本更新采用 Welford 在线算法（不使用「平方和减均值平方」的朴素公式）：

```
n     = n + 1
delta = x - mean
mean  = mean + delta / n
m2    = m2 + delta * (x - mean)   // 离差平方和，恒非负
```

总体方差 = `m2 / n`，样本方差 = `m2 / (n - 1)`。

两个累加器合并采用 Chan 等人的并行公式：

```
n     = na + nb
delta = meanB - meanA
mean  = meanA*(na/n) + meanB*(nb/n)
m2    = m2A + m2B + delta^2 * na*nb / n
```

合并对交换逐位稳定（`Merge(a, b)` 与 `Merge(b, a)` 结果逐位相同）：
均值写成两个对称乘积之和，并用显式 `float64` 转换阻断 FMA 融合、
把 `na*nb` 先算成单一乘积以规避乘法不可结合性。

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
