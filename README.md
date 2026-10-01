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

## 搜索片段选取器（`snippets` 包）

`snippets.Registry` 提供并发安全的文档登记与贪心片段选取：

- `Register(docID, text)`：登记文档；`text` 必须是合法 UTF-8 且不超过
  `snippets.MaxTextLen`（1048576）字节，保存独立副本。
- `Unregister(docID)`：删除文档。
- `Snippets(docID, hits, W, K)`：窗口宽度 `W∈[1,4096]`，片段数 `K∈[1,16]`，
  命中数（去重前）不超过 `snippets.MaxHits`（10000）。命中为字节半开区间
  `[Start,End)`，两端必须落在 UTF-8 字符边界（`End==len(text)` 合法）。

### 选取轮次

每轮只考虑**可用命中**——与全部已选片段不相交（半开区间判定
`s < 片段终点 && 片段起点 < e`，相接不算相交）。对可用命中的每个不同起点 `a`：

1. 窗口右端 `r = min(a+W, len(text), 起点大于 a 的已选片段的最小起点)`；
2. 包含集 `C(a) = { 可用命中 h | h.s >= a 且 h.e <= r }`（`h.e == r` 包含）；
3. 分值为 `|C(a)|`，完全相同的 `(s,e)` 先去重，其余互相重叠的命中各自计一；
4. 取分值最大者，并列取 `a` 最小；最大分值为 0（没有命中能放进任何窗口）时结束。

选中片段为 `[a, E)`，其中 `E = max{h.e | h ∈ C(a)}`。选满 `K` 个或无可用命中
时结束；返回结果按 `Start` 升序（非选取顺序），每项含 `Start`、`End`、
`Highlights` 与 `Score`。

### 高亮合并

`Highlights` 是 `C(a)` 按起点升序后合并**重叠**区间的结果：仅当
`s2 < e1` 时合并，相接的 `[1,3)` 与 `[3,5)` 保持为两个高亮区间。

### 拒绝原因（只报按序第一个，拒绝不改变状态）

- `Register`：`ErrInvalidArgument`（空 docID / 非法 UTF-8 / 超长）→
  `ErrDuplicateDocument`（docID 已存在）。
- `Unregister`：`ErrDocumentNotFound`。
- `Snippets`：`ErrDocumentNotFound`（docID 未登记）→ `ErrInvalidArgument`
  （W/K 越界、hits 去重前超限、命中越界、`s>=e` 或端点不在字符边界）。
  `hits` 为空合法，返回空结果。

### 本地验证

```bash
# 全量测试（含规定场景与 2000 组随机对拍）
go test ./snippets -count=1

# 打印随机对拍的输入、输出与判定依据（日志另存到 snippets/testlogs/diff.log）
go test ./snippets -run TestRandomDifferential -v -count=1 | tee snippets/testlogs/diff.log

# 竞态检测（验证 Register/Unregister/Snippets 并发等价于某串行顺序）
go test -race -count=1 ./...
```

随机对拍把生产实现与严格按上述规则逐轮写成的朴素实现
（`naiveSnippets`，见 `snippets/naive_test.go`）做 `reflect.DeepEqual` 比较，
输入命中会随机打乱并插入重复，以保证与给出顺序无关、去重正确。
