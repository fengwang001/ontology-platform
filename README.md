# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前交付：**链接基数约束与属性级权限联合仲裁子系统**（包 `ontology/`），
负责链接创建/删除时把「两端基数上限」与「操作者对两端来源属性的继承/覆盖可见性」
按统一优先级联合裁决，并把拒绝原因归一化为四类可区分错误。

## 子系统概览

- `ontology/catalog.go`：对象类型、实例、链接类型与类型层属性默认（共享模式目录）。
- `ontology/ledger.go`：链接基数账本，只维护物理链接与两端占用计数。
- `ontology/permission.go`：角色层级继承 + 实例层覆盖解析（最近距离优先、同距离取最新声明）。
- `ontology/arbiter.go`：联合仲裁、错误归一化、串行日志、可见性枚举与确定性重放。
- `ontology/naive.go`：独立朴素对照模型（全量重算），仅供随机差分测试。

错误优先级（创建）：`ERR_INVALID_PARAM` → `ERR_INVISIBLE`
→ `ERR_SOURCE_CARDINALITY_EXCEEDED` → `ERR_TARGET_CARDINALITY_EXCEEDED`，只报第一个命中项。
删除时「不可见」与「不存在」合并为同一个 `ERR_NOT_FOUND`，不泄露链接存在性。

设计取舍、被放弃方案与复杂度证明见 `docs/DESIGN.md`，最小 API 示例见 `docs/USAGE.md`。

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

# 逐条打印输入 / 实际输出 / 判定依据
ONT_TEST_ECHO=1 go test -v ./ontology

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
