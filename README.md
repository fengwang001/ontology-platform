# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology`：逻辑删除对象的溯源审计与复活协调器。对象生命周期由若干段
  存活区间构成，审计事件精确归属到区间；复活需显式指向延续的删除事件，
  并按链接类型声明恢复满足条件的链接。设计取舍见 [docs/DESIGN.md](docs/DESIGN.md)。
- `cmd/server`：演示程序，走一遍创建、建链、删除、复活并打印审计历史。

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

# 性能基准（链接恢复条件判断与删除复活循环次数无关）
go test -run=NONE -bench=RestorableCheck -benchmem ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
