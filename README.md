# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `fat12/`：带连续优先分配与碎片整理的 FAT12 簇链表管理器。12 位打包
  字节映像、连续段优先 + 循环下一适应的分配策略、rover 移动规则、
  `Defrag` 规则、错误拒绝顺序与本地验证方法见 `fat12/README.md`。

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
