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

## 路径锁服务（pathlock）

仓库级大文件路径锁：协作者推送二进制文件前须先对路径加锁，服务负责锁的
创建、查询、释放、管理员强制释放，以及推送校验阶段对一批路径的全有或全无持锁核验。

- 设计与取舍说明见 [pathlock/DESIGN.md](pathlock/DESIGN.md)。
- 核心接口：`Service.Lock` / `Service.Unlock`（`force` 为管理员强制释放并记审计）/
  `Service.ValidatePush`（`releaseOnPass` 为「校验并顺带释放」）/ `Service.ListByPrefix`（前缀分页）。
- 路径先经 `NormalizePath` 规范化；祖先与后代路径对他人互斥。
- 运行测试：`go test ./pathlock -race -v`。
