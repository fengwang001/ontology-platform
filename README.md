# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 占道施工许可审查服务（`permit` 包）

实现路段车道封闭申请的受理与冲突审查、指定绕行路线双向冲突、走廊级并发封闭
上限、应急抢修对已批常规许可的抢占与顺延恢复、许可延期与撤销，并保证每份许可
在任意时刻的状态与每次拒绝原因都可精确复现。

- 设计取舍与被放弃方案：[permit/DESIGN.md](permit/DESIGN.md)
- 包 API 说明：见 `permit/doc.go` 包注释
- 独立朴素全量重判对照模型：`permit/naive`
- 测试：`go test ./permit`（规定场景）、随机差分（120 种子×500 操作，逐条对照朴素模型）、
  查询复杂度验证、重放确定性、并发串行化；`-v` 下差分日志打印每条操作的输入、输出与判定依据。

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
