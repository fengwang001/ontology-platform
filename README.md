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

## 模块

- `triplesync/`：以稳定文件编号为身份的三方（基线/本地/远端）目录同步
  计划器。属性级三方判定、目录删除否决与复活、同名 `.c<id>`/`x` 消解、
  确定性动作排序。规则与验证方法见 `triplesync/README.md`，其中包含
  2000 组随机快照与朴素参照实现对照的测试入口。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
