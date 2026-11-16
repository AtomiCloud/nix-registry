module github.com/AtomiCloud/obs

go 1.27.4

require (
	github.com/VictoriaMetrics/VictoriaLogs v1.54.0
	github.com/VictoriaMetrics/VictoriaMetrics v1.156.0
	github.com/VictoriaMetrics/metrics v1.45.0
	github.com/VictoriaMetrics/metricsql v0.87.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/VictoriaMetrics/easyproto v1.2.0 // indirect
	github.com/VictoriaMetrics/fastcache v1.13.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.20.4 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	github.com/valyala/fastrand v1.1.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/valyala/gozstd v1.27.0 // indirect
	github.com/valyala/histogram v1.2.0 // indirect
	github.com/valyala/quicktemplate v1.8.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

// VictoriaLogs v1.54.0 comes from the tag's commit, never through the Go module
// proxy: the proxy holds a 2021 VictoriaMetrics commit under that version. The copy
// is third_party/VictoriaLogs (see third_party/README.md).
replace github.com/VictoriaMetrics/VictoriaLogs => ./third_party/VictoriaLogs
