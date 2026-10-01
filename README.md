# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 提交图合并基选择器

`ontology` 包提供并发安全的提交图：`Commit` 登记带父提交，`Gen` 查询世代号
（根为 1，否则为 `1 + max(父世代号)`），`IsAncestor` 判定祖先（含自身），
`MergeBases` 返回两个提交的**全部**合并基（公共祖先集合的极大元，按 id 字节序
升序）。交叉合并可返回多个合并基；互不相连时返回空集而非错误。

`MergeBases` 采用双向洪泛并以世代号剪枝：在 N 个提交的线性主干末端分出两条各
10 个提交的分支时，遍历数恒为 21，N=1000 与 N=100000 完全相同，与历史总长度
无关。定义、剪枝依据、错误优先级与对拍说明见 `ontology/DESIGN.md`。

```bash
go test -race -v ./ontology/                       # 全部用例 + 2000 组随机对拍
go test -v -run TestGenerationPruning ./ontology/  # 世代号剪枝计数证明
```

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
