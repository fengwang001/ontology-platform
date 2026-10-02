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

## 变更缓冲（change buffer）

带空闲空间估计位图的变更缓冲模型位于 `changebuffer/`：对不在池中的二级
索引页缓冲 `Insert`/`DeleteMark`/`Purge`，在页读入或估计空间不足时按到达
序合并。空闲桶、保证下界、缓冲/强制合并判定、条目语义、不变式与本地验证
方法见 `changebuffer/README.md`。

```bash
GOCACHE=/tmp/gocache go test -race ./changebuffer/
GOCACHE=/tmp/gocache go test ./changebuffer/ -run TestRandomDifferential -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
