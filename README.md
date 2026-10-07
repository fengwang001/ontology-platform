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

## 分块快照完整性校验与跨类型聚合导出

实现位于 `snapshot/`：按对象类型拆分块文件、每块自带 SHA-256 与声明条数，
加载时分阶段判定块完整性、数量一致性、跨块引用（目标块不可信时给出保守的
“无法校验”结论），聚合视图全有或全无、单块可独立取用，全部校验只读幂等。

- 设计说明（关键取舍、被放弃方案、验证方法）：`docs/design.md`
- 磁盘格式：`docs/format.md`
- 可运行演示：`go run ./cmd/snapshotdemo -damage none|flip|count|dangle|targetcount`

若 `~/.cache/go-build` 只读，指定临时构建缓存：`export GOCACHE=/tmp/gocache`。
