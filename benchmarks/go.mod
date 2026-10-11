module github.com/data-bet/simdjson-go/benchmarks

go 1.27.0

require (
	github.com/buger/jsonparser v1.6.1
	github.com/bytedance/sonic v1.15.4
	github.com/data-bet/simdjson-go v0.0.0-00010101000000-000000000000
	github.com/klauspost/compress v1.20.1
	golang.org/x/perf v0.0.0-20261009192801-be2c69fb417e
)

replace github.com/data-bet/simdjson-go => ../

require (
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic/loader v0.5.2 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.32.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

tool golang.org/x/perf/cmd/benchstat
