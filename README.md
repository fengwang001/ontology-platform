# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 货位分配系统（`slotting`）

`slotting/` 实现入库上架的货位分配：在承重、净高、品类、混批、相邻易燃/食品隔离、
货位冻结与容量共同限制下自动/指定分配货位，支持移库、取出、批量（全有或全无）与一致快照查询，
并发可线性化、重放确定。设计取舍、被放弃方案与复杂度证明见 [`slotting/DESIGN.md`](slotting/DESIGN.md)。

```bash
go test ./...                      # 全量测试（含朴素模型随机差分）
go test -race ./...                # 竞态检测
go test -run TestExaminedSublinear -v ./slotting   # 考察货位数 O(sqrt N) 的可验证证据
go run ./cmd/demo                  # 演示：打印输入、输出与判定依据
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
