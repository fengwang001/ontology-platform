# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## indexorder：索引序满足判定器

`indexorder` 包登记带方向与空值位置的多列索引，并为给定的等值列集合
与 ORDER BY 列表挑选能免去排序的索引与扫描方向。

### 数据模型

- 索引为 `(名字, 列项序列)`，列项为 `(列名, 方向 ASC/DESC, 空值位置 NULLS FIRST/LAST)`。
- 索引键按列项序列依次比较。
- `Registry` 提供 `Register` / `Drop` / `Choose`，三者均可并发调用，
  结果等价于某个串行顺序；`Choose` 为只读。

### ORDER BY 规整（need）

`Choose(eq, order)` 先规整 `order` 得到 `need`：

1. 丢弃列名属于 `eq` 的项（无论其方向与空值位置）；
2. 对重复出现的列名只保留第一次出现的项。

`need` 为空时任何索引都以正向满足。

### 满足条件

索引 `I` 以方向 `d`（正向/反向）满足 `need`，当且仅当依次考察 `I`
的各列项 `c`：

- 列名属于 `eq` 的列项跳过；
- 否则 `c` 必须与 `need` 中下一个未匹配项 `n` 列名相同，且
  - 正向：`c` 的方向与空值位置都等于 `n` 的；
  - 反向：`c` 的方向与空值位置都等于 `n` 的取反
    （ASC↔DESC，NULLS FIRST↔NULLS LAST）；
  不符则 `I` 对该方向不满足；
- `need` 全部匹配后立即满足，后续列项不再考察；
- `I` 的列项走完而 `need` 仍有剩余则不满足。

### 选择次序

多个索引都能满足时依次比较：

1. 能以正向满足者优先于只能以反向满足者；
2. 列项数少者优先；
3. 名字字节序小者优先。

返回被选索引的名字与扫描方向（`Forward` / `Backward`）。

### 错误

- `Register` 按「名字为空 → 名字已存在 → 列项序列为空 → 某列名为空 →
  某列项方向或空值位置非法 → 同一索引内列名重复」的顺序只报第一个错误。
- `Drop` 对不存在的名字报 `ErrIndexNotFound`。
- `Choose` 的非法输入（`eq` 含空列名、`order` 列名为空、`order` 方向或
  空值位置非法）先于「没有任何已登记索引满足」报出，各类原因均为
  可 `errors.Is` 判定的哨兵错误。
- 被拒绝的操作不改变已登记的索引集合。

### 本地验证

```bash
go test ./indexorder/                 # 单元测试 + 2000 组随机对拍
go test -race -v ./indexorder/        # 竞态检测并打印每组对拍的输入/输出/判定依据
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
