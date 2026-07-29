module github.com/minio/simdjson-go/benchmarks

go 1.25.0

require (
	github.com/buger/jsonparser v1.2.0
	github.com/bytedance/sonic v1.15.2
	github.com/klauspost/compress v1.19.1
	github.com/minio/simdjson-go v0.0.0-00010101000000-000000000000
)

replace github.com/minio/simdjson-go => ../

require (
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic/loader v0.5.1 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.29.0 // indirect
	golang.org/x/perf v0.0.0-20260709024250-82a0b07e230d // indirect
	golang.org/x/sys v0.47.0 // indirect
)

tool golang.org/x/perf/cmd/benchstat
