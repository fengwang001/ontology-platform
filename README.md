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

## 学分替换与毕业审核引擎（`gradaudit`）

按培养方案要求树核对修读记录，覆盖课程替换、转入学分、重修/多次修读、重复计入、
毕业附加条件与确定性归因。

```bash
# 全量测试（含竞态检测）
GOCACHE=/tmp/gocache go test -race ./gradaudit/

# 朴素模型随机对照（-v 打印每步输入/输出/判定依据；可用种子复现）
GOCACHE=/tmp/gocache DIFF_SEED=42 go test -v -run TestNaiveDifferential ./gradaudit/
```

- 设计取舍与本地验证：`gradaudit/DESIGN.md`
- API 与语义：`gradaudit/doc.go`
- 错误固定优先级：`ErrInvalid` > `ErrNotFound` > `ErrAlreadyRevoked` >
  `ErrSubNotApplicable` > `ErrTransferOverflow` > `ErrVersionTooOld`
