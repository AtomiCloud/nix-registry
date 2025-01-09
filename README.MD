# AtomiCloud Nix Registry

AtomiCloud's Nix Registry for custom built packages. 


# Packages

- semantic generator
- upstash
- action-docs
- typescript-json-schema
- swagger-typescript-api
- dotnetsay
- dotnet-ef
- mirrord
- pls
- toml-cli
- nix-share
- aws-export-credentials
- cyanprint

# Installation

To use latest registry

```bash
nix shell github:AtomiCloud/nix-registry
```

To add to flakes
```nix
atomipkgs.url = "github:AtomiCloud/nix-registry";
```
