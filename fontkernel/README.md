# fontkernel — 网页字体匹配 / 子集加载 / 回退渲染协调内核

给定字体族声明、加载进度与逻辑时钟，内核可以对任意文本逐字符精确复现：
该字符由哪张人脸渲染、处于阻塞期（不可见占位）、交换期（回退渲染）、
已加载还是永久回退。

## 五个协作部分

| 部分 | 文件 | 职责 |
| --- | --- | --- |
| 字体族登记 | `registry.go` | 参数校验、重复人脸拒绝、构造覆盖索引 |
| 人脸匹配 | `match.go` | 宽度 → 倾斜 → 字重三维字典序匹配 |
| 字符范围子集 | `tree.go` | 质心区间树，点刺穿查询 O(log N + k) |
| 加载状态期 | `engine.go` | 阻塞/交换/放弃状态机、单调时钟 |
| 文本整形 | `engine.go` | 逐字符裁决、触发胜者、回退、段落合并 |

## 快速使用

```go
e := fontkernel.New(fontkernel.DefaultConfig(), os.Stderr)
e.RegisterFamily(fontkernel.FamilySpec{
    Name: "sans",
    Faces: []fontkernel.FaceSpec{{
        WeightLo: 400, WeightHi: 400,
        WidthLo: 100, WidthHi: 100,
        Runes: []fontkernel.RuneRange{{Lo: 'a', Hi: 'z'}},
        Policy: fontkernel.PolicySwap, URL: "sans.woff2",
    }},
})

res, _ := e.Shape(fontkernel.ShapeRequest{
    Family: "sans", Fallback: []string{"sys"},
    Text: "hello", Weight: 400, Width: 100,
})
e.ReportLoaded("sans", 0, 50)   // 资源到达
e.Advance(100)                  // 推进逻辑时钟
```

`ShapeResult.Runes` 给出每个字符的胜出族/人脸、渲染族/人脸、时期、是否合成斜体
与最后手段标记；`Segments` 是连续相同渲染身份的合并段。

## 关键语义

- 匹配只在覆盖目标字符的人脸中进行；宽度等距时按“宽于正常偏好宽、否则偏好窄”
  破歧；斜体只允许由正常脸单向合成；字重三段规则与两阈值均可配置。
- 人脸资源在其范围与文本有交集时才开始加载，起算时刻取**首次触发**；
  同一字符落在多张人脸中时只触发匹配胜者。
- 四种策略的阻塞/交换期均可配置；期边界左闭右开；可选策略期满放弃后，
  迟到的加载完成被拒绝且永不替换。
- 回退族按次序取第一个已就绪且覆盖字符的族；回退整形不触发回退族自身加载；
  全部不覆盖时输出最后手段标记。
- 错误六类（`ErrInvalidArgument` / `ErrClockRewound` / `ErrFamilyMissing` /
  `ErrFaceMissing` / `ErrDuplicate` / `ErrInvalidLoadOp`）可区分，
  拒绝次序固定，被拒操作不改变登记、状态与时钟。

## 测试与验证

```bash
go test -race ./fontkernel/
go test -run TestNaiveDifferentialRandom -v ./fontkernel/   # 朴素模型随机对照
go test -run TestCoverageLookupSublinear -v ./fontkernel/   # O(log N) 计数证据
go test -bench=. ./fontkernel/
```

传入非 nil 的 `io.Writer` 给 `New` 即可打印每次操作的输入、输出与判定依据
（`SHAPE input` / `TRIGGER` / `EXPIRE` / `SHAPE rune` / `SHAPE output`）。
