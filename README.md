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

## 块文件系统映像检查与修复（fsck）

`fsck` 包提供块文件系统映像的一致性检查与修复，详见
[docs/fsck.md](docs/fsck.md)。

```bash
# 测试（含竞态检测与判定日志）
go test -race -v ./fsck/

# CLI
go run ./cmd/fsck format /tmp/fs.img -blocks 64 -inodes 16
go run ./cmd/fsck check /tmp/fs.img
go run ./cmd/fsck repair /tmp/fs.img
```
