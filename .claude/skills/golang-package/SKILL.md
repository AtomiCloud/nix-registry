---
name: golang-package
description: Add or upgrade Go packages in the nix-registry
trigger: /golang-package
---

# Go Package

Guide for working with Go packages (using buildGoModule).

> **Note:** This project uses `pls` instead of `task` for running Taskfile commands (e.g., `pls gen:go:sha`, `pls gen:go:vendor:sha`).

See [Golang Guide](../../docs/developer/packaging/Golang.md) for complete documentation on:

- Adding new Go packages
- Upgrading existing packages
- Using `pls gen:go:sha` and `pls gen:go:vendor:sha`
- CI integration
