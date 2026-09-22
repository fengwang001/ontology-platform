package serve

import "ontology/multipart"

// boundaryChooser 选择边界串。生产路径使用 multipart.ChooseBoundary
// （crypto/rand）；测试可替换为确定性实现，以便逐字节对照不同切分下的输出。
var boundaryChooser = multipart.ChooseBoundary
