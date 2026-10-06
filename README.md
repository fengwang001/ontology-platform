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

## 稀疏检出规则引擎

包 `ontology`（根目录）实现了一个并发安全的稀疏检出规则引擎。

### 规则模式

- 精确路径：`a/b/f`，只命中自身。
- 目录前缀：`a/b/`，命中该目录及全部后代（任意深度）。
- 本层所有：`a/b/*`，只命中 `a/b` 的直接子项，不穿透更深层。

规则有序，对任一路径取**最后一条命中者**；无规则命中默认不物化。
文件物化则其全部祖先目录隐含物化；没有物化文件后代的目录即使被包含也不物化（空目录）。

### 主要 API

- `NewEngine()` / `AddCommit(id, files)`：创建引擎、注册提交。
- `ReplaceRules(rules, commitID, force)`：校验并替换规则集（产生严格递增新版本），
  `commitID` 非空时在同一次原子调用里切换提交；返回 `(新版本号, Diff, 被丢弃修改路径, err)`。
- `ApplyRuleset(commitID, version, force)`：仅切换提交或仅应用已注册规则集版本。
- `MarkDirty(path)` / `ClearDirty(path)`：设置、清除本地修改标记。
- `QueryPath(path)`：返回五种互斥状态之一
  （物化 / 因规则排除 / 因无规则命中 / 因目录为空 / 路径在提交中不存在）。
- `ListMaterialized(prefix)`：按字节序列出前缀下全部物化路径。

### 原子性与受阻

撤销物化（或切换提交后消失）的路径若带本地修改，整次变更被拒绝并返回
`*BlockedError`（受阻路径按字节序、去重），提交、规则集版本、物化集合与脏标记都不变；
`force=true` 时丢弃这些修改并继续，丢弃路径在返回值中列出。错误优先级为：
参数非法 → 提交不存在 → 规则集版本不存在 → 受阻于本地修改。

### 设计与性能

详见 [`DESIGN.md`](DESIGN.md)。引擎以单路径祖先链索引判定命中，
以子树指纹联合剪枝计算变更影响面，目录物化用引用计数增量维护，
使单路径查询与单次变更的开销都不随无关文件/未受影响物化路径数增长。
