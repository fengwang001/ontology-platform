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

## 批量导入校验钩子可见性子系统

实现位于 `ontology/` 包，设计细节见 `ontology/DESIGN.md`。

- 批内可见性严格按输入列表顺序（前缀语义，既非开始前快照也非结束后最终态）。
- 支持 `ALL_OR_NOTHING` 与 `BEST_EFFORT` 两种整体语义，以及仅在全有全无语义下
  提交前触发一次的批次级后置钩子。
- 错误按「参数非法 → 前置钩子 → 后置钩子」归一化，三类可区分。
- 单次可见性解析为 O(1) 哈希探测，不随批次长度增长（`AccessCounters` 可验证）。
- 跨批次以快照 + 版本检测 + 冲突重放保证可串行化与重放确定性。

验证：

```bash
go test -race -v ./ontology/
go test -run TestRandomDifferential -count=5 ./ontology/  # 2000 组随机差分对照朴素模型
```
