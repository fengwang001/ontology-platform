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

## 告警分组通知器（alertstore / suppress / notify）

设计与取舍见 `DESIGN.md`（含被放弃的方案）。

```bash
# 本机 go 若不在 PATH，先指定安装目录
export PATH=$PATH:/usr/local/go/bin
# 若默认 GOCACHE 只读，重定向到 /tmp
export GOCACHE=/tmp/gocache

go build ./...                 # 编译
gofmt -l .                     # 无输出即格式正确
go vet ./...                   # 静态检查
go test -race -count=1 -v ./...   # 全量测试（日志含输入/输出/判定依据）
go test -race -run TestOracleFuzz ./notify   # 与朴素模拟逐步对照
```

- `alertstore`：指纹/分组键、firing/resolved 记录、Amax 上限。
- `suppress`：半开静默区间与非递归抑制（源资格不受其自身状态影响）。
- `notify`：单调时钟、错误优先级、分组节奏 (a)/(b)/(c)、失败整组保留与重试。
