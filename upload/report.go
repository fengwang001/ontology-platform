package upload

// Report 描述一次上传当前的登记账目。
type Report struct {
	Received int   // 当前已登记的分片数
	Bytes    int64 // 已登记分片的字节总和
	Replaced int   // 累计被覆盖的分片次数
	Missing  []int // 完成校验时缺少的分片号，升序
}
