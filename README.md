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

## 属性索引重建审计溯源（`ontology` 包）

`ontology/` 实现带审计溯源的属性索引重建：以全局单调序号（非墙钟时间）精确划分
基线/增量范围，重建产出含逐条来源映射与 SHA-256 指纹的审计记录；复核者仅凭审计
记录与对象当前状态即可逐条独立判定（每条目 O(1) 当前态读、0 历史读），并区分
“索引不存在 / 重建中不可用 / 可用但带不一致声明 / 条目级不一致”。

设计取舍、被放弃方案与需求-测试对应关系见 `ontology/DESIGN.md`。

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test -race -v ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
