# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## PDB 驱逐裁决服务

`pdb` 包实现面向节点排空的 Pod 中断预算（PDB）驱逐裁决：单个驱逐、整批
排空、驱逐确认/取消与宽限到期自动回补。详见 [`pdb/DESIGN.md`](pdb/DESIGN.md)。

```bash
# 随机差分（索引实现 vs 独立朴素参考模型）、复杂度结构证明、竞态
go test -race -v ./pdb
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
