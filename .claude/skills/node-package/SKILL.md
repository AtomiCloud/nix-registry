---
name: node-package
description: Add or upgrade Node.js/npm packages in the nix-registry
trigger: /node-package
---

# Node.js Package

Guide for working with Node.js packages (built the official nixpkgs way with `buildNpmPackage`).

> **Note:** This project uses `pls` instead of `task` for running Taskfile commands (e.g., `pls gen:node:22`).

Packages are declared in `node/22/node-packages.json` (the manifest) and built
via `node/22/build-npm.nix` + `export.nix`. Registry tarballs ship pre-built
output but no lockfile, so `pls gen:node:22` vendors a prod-only
`package-lock.json` per package and pins the tarball + npm-deps hashes.

See [Node Guide](../../docs/developer/packaging/Node.md) for complete documentation on:

- Adding new Node.js packages
- Upgrading existing packages
- Using `pls gen:node:22` for lockfile/hash generation
- CI integration
