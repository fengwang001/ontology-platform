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

## 块级增量备份链

`backup` 包实现定长块卷的全量/增量备份链：增量只存与父备份还原结果
不同的块，删除中间备份时把其块并入直接后继并剔除与新父相同的块，
保证删除前后保留备份的还原结果逐字节不变；还原会话打开期间其回溯
路径上的备份禁止删除。规则说明与本地验证见 `backup/README.md`。
