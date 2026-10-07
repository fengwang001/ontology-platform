# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `reconcile/`：多副本快照对账与和解组件。将各副本独立产出的快照归并为唯一确定的
  和解结果：以最早逻辑位点为基准、按副本自带可比较标识（Priority）裁决冲突、
  隔离损坏副本、标识并列时判定对象不可和解。设计与取舍详见
  [reconcile/DESIGN.md](reconcile/DESIGN.md)。

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
