# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `allocation/`：高校教师授课任务分配与学期工作量核算服务
  （任务指派、时段冲突、待确认/惰性超时、批量原子指派、中途换人、
  学期欠额/超额与抵扣结转、幂等冻结）。设计与取舍见 [`docs/DESIGN.md`](docs/DESIGN.md)。

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
