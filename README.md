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

## 通知模板版本化存储与渲染（`notify` 包）

`notify.Store` 支持按语言回退链、渠道回退与生效时刻选择模板版本，
并在变量缺失或渲染超长时沿候选链继续回退。构造参数 `dl` 为默认语言，
必须是合法语言标签，否则 `New` 会 panic。

### 语言标签与回退链

- 标签由 1–3 个以 `-` 连接的子标签组成，区分大小写：
  - 第 1 个：2 或 3 个小写字母；
  - 第 2 个：4 个字母且首字母大写其余小写（文字，如 `Hant`），
    或 2 个大写字母（地区，如 `TW`）；
  - 第 3 个只允许跟在文字之后，为 2 个大写字母。
- 回退链：语言自身 → 依次去掉最后一个子标签 → 只剩首个子标签 →
  追加 `dl`（已在链中则不重复）。例如 `dl=en` 时
  `zh-Hant-TW → zh-Hant → zh → en`；`zh-TW` 不回退到 `zh-Hant`。

### 候选顺序与版本选择

- 候选序列以回退链语言为**外层**顺序，渠道 `[请求渠道, *]` 为**内层**顺序，
  即语言优先级高于渠道具体性。
- 每个 `(name, loc, ch)` 的版本按 `eff` 排序；取 `eff <= at` 的最大 `eff`。
  `eff == at` 命中，`eff == at+1` 不命中。
- 相同 `eff` 后登记的覆盖先登记的（正文与墓碑互相覆盖）。
- 墓碑（`Retire`）使该键在对应时刻视为无候选而被跳过，但**不阻断**
  链上其余候选。
- 版本数组保持有序、`eff` 唯一，查找使用二分；包内非导出计数器
  （`probeSnapshot`、`renderKeyLookups`）证明单键探测次数不超过
  `ceil(log2(版本数+1))`，单次 Render 的键查找数不超过回退链长度的 2 倍。

### 正文语法、变量与转义

- `{{` 与 `}}` 为字面 `{`、`}`；`{v}` 为必需变量，`{v|text}` 为带默认文本的
  变量（`text` 可空，不含 `{`、`}`、`|`）。
- 变量以紧随的第一个 `}` 结束，因此 `{a}}` 是变量 `a` 后跟单个 `}`，
  语法错误偏移为 3；其余单个 `{`/`}` 均为语法错误，报告自左向右扫描的
  首个错误字节偏移。
- 变量存在且值为空串算存在；缺失仅适用于无默认文本的必需变量，
  缺失名按升序报告。`{v|text}` 在变量缺失时取 `text`。
- 转义只作用于**变量值**，且只看**请求渠道**：请求渠道为 `email` 时
  把 `& < > "` 依次转义为 `&amp; &lt; &gt; &quot;`（故 `*` 模板渲染到
  email 也会转义）；默认文本与正文字面文本永不转义；sms/push 不转义。
- 长度上限按渲染结果的码点数：sms 70、push 100（恰等不超长）、email 不限。

### 失败原因与拒绝优先级

全部候选失败时：无任何候选报「无模板」；否则按**第一个候选**的原因报
缺变量（升序列表）或超长（码点数）。拒绝原因按以下顺序只报第一个：

- `Publish`/`Retire`：参数非法 → 语言非法 →（仅 Publish）语法错误；
  body 还要求非空、合法 UTF-8、不超过 1000 字节。
- `Render`：参数非法（name、`at` 非负、ch 必须是具体渠道、vars 键名合法）
  → 语言非法 → 无模板 → 缺变量 → 超长。
- 被拒绝的 `Publish`/`Retire` 不改变任何版本。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -count=1 ./...

# 查看 2000 组随机序列对拍的输入/输出/判定依据日志
go test -race -v -run TestFuzzAgainstOracle2000 ./notify/

# 格式与静态检查
gofmt -l .
go vet ./...
```

对拍测试（`TestFuzzAgainstOracle2000`）以固定随机种子重放 2000 组
Publish/Retire/Render 混合序列，并与独立编写的朴素线性扫描参考实现
逐条比较结果；相同登记序列重放结果恒定（`TestReplayDeterminism`），
并发安全由 `TestConcurrentOperations` 在 `-race` 下覆盖。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
