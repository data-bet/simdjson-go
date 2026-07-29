module github.com/minio/simdjson-go/benchmarks

go 1.25.0

require (
	github.com/buger/jsonparser v1.2.0
	github.com/klauspost/compress v1.19.1
	github.com/minio/simdjson-go v0.0.0-00010101000000-000000000000
)

replace github.com/minio/simdjson-go => ../

require (
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
