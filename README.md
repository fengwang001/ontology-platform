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

## 包说明

- `fsck`：块文件系统映像的一致性检查与修复器。扫描索引节点表、块位图
  与目录项，按固定顺序修复共享块、悬空目录项、多父目录、不可达节点、
  链接计数与位图不一致；修复在副本上进行并原子替换，支持并发只读检查
  与写挂载互斥。详见 [fsck/README.md](fsck/README.md)。
