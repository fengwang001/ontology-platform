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

> 若默认 Go 构建缓存所在文件系统只读，可设置 `export GOCACHE=/tmp/gocache`。

## 版本向量增量反熵同步（`vvsync`）

`vvsync/` 实现基于版本向量的增量反熵同步：每条本地写入由 `(来源副本, 序号)`
唯一标识；同步时目标发送版本向量，来源只回送序号超过目标已见值的最小差集，
按 `(来源, 序号)` 有序发送，目标校验序号连续后原子应用。读视图对每个键取
`(时间戳, 来源, 序号)` 字典序最大者。未注册副本、变更不连续、向量非法、
日志超限四类非法输入整体拒绝，原因互不相同且失败不留痕。任意同步顺序下
向量、日志与视图收敛一致，支持多执行体并发调用。

详细规则、错误类别与验证说明见 `vvsync/DESIGN.md`。

```bash
go test -race -v ./vvsync/
```
