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

## 专业技术职称评审会务服务

评审会务（评委抽取与回避、分轮表决、中途回避替补、公示异议、
终局生效）实现于 `review/` 包，完整设计、关键取舍、被放弃方案与
需求逐条验证方法见 `docs/design.md`。

```bash
export GOCACHE=/tmp/go-cache        # 如默认构建缓存目录只读
go test -race -count=1 ./...
go test -cover ./...

# 朴素模型随机差分的逐行日志（输入 / 两侧输出 / 判定依据）
REVIEW_DIFF_LOG=1 go test ./review/ -run TestDifferential -v
```
