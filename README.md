# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `lsm`：分层日志结构存储的压实任务选择与安装服务。覆盖各层文件登记、
  按层精确有理数打分选层、零层/非零层两种起点、端点相接的边界闭合、
  下一层重叠纳入、在途占用冲突改选、直接下移与压实结果安装。
  设计取舍与验证方法见 [lsm/DESIGN.md](lsm/DESIGN.md)。

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
