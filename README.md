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

## 备份重建裁决组件

四类备份（对象类型定义、对象实例、链接实例、动作执行记录）的重建顺序裁决与
可重建范围判定位于 `restore/` 包：

- 设计说明与关键取舍见 `docs/design.md`；
- API 用法见 `docs/usage.md`；
- 朴素参照模型与 400 组随机损坏对照见 `restore/naive.go`、
  `restore/fuzz_compare_test.go`，逐案审计落盘于 `testdata/fuzz/cases.jsonl`。

```bash
go test ./...
go test -race -v ./...
go test -run TestFuzzAgainstNaiveReference ./...
```
