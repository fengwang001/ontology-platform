# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 麻醉与精神类药品专用账册

实现位于 [`narledger/`](narledger/)（设计与取舍见 `narledger/DESIGN.md`），覆盖：

- 入库登记（批号唯一、效期判定 `now < expireAt`）、双人复核领用（FEFO 跨批次、全有或全无）；
- 使用后闭环结清、差额待处理锁定与两名复核人确认、批次销毁见证；
- 左闭右开授权区间与提前撤销；单调时钟与严格错误优先级；
- 选批与科室锁定判定均不随历史规模增长（两档规模对照可验证）；
- 与独立朴素模型 1500 组随机序列差分对照、并发串行等价、账面不变量核验。

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
