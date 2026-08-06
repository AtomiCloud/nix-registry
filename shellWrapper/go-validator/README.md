# go-validator

`go-validator` supplies the offline runtime shared by Go lint and dead-code
commands. A consumer declares its own source and vendor hash, then passes the
resulting `buildGoModule.goModules` directory explicitly.

```bash
go-validator run --proxy "$goModules" -- golangci-lint run --timeout 5m ./...
go-validator run --proxy "$goModules" -- bash ./scripts/local/deadcode-whole.sh
```

Its runtime closure lists every binary individually rather than using a bundle:
a bundle plus a member collides in `buildEnv`, even if `mkShell` masks it by
selecting one PATH entry.
