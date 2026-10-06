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

## 联程座位库存（inventory 包）

多航段联程座位库存与超售控制系统，见 `DESIGN.md`。

```bash
# 全部测试（含朴素对照模型随机比对与并发测试）
go test ./inventory/ -race

# 查看随机比对逐步日志（输入/输出/判定依据）
go test ./inventory/ -run TestRandomAgainstNaiveModel -v

# 性能证明基准（过期积压 1k/10k/100k 下开销平坦）
go test ./inventory/ -bench Backlog
```
