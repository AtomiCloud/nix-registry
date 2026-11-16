# third_party

`VictoriaLogs/` is VictoriaLogs v1.54.0 (tag commit `6433dae586cc15921b4b9c50a805a49553c9d761`, Apache-2.0),
the files obs needs: the tag's `go.mod`, `go.sum`, `LICENSE`, `lib/logstorage` and `lib/prefixfilter`,
without tests. `go.mod` points at it with a `replace`: the Go module proxy holds a 2021 VictoriaMetrics commit
under the version `v1.54.0`, so `go get` cannot fetch it.

- `VictoriaLogs.sha256` lists the sha256 of every file of the tag's tree.
- The tree equals the tag except one added file, `VictoriaLogs/lib/logstorage/obs_fieldnames.go`
  (marked `OBS CHANGE`, Apache-2.0 section 4). `go test ./internal/treecheck` and `tests/run.sh` fail on any
  other difference.
- To bump: see `golang/obs/README.md`, section "src/third_party/VictoriaLogs".
