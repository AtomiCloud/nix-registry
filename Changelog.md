## [2.4.0](https://github.com/AtomiCloud/nix-registry/compare/v2.3.0...v2.4.0) (2025-05-07)


### ✨ New Packages ✨

* **openapi2postmanv2:** convert openapi to postman ([f47a429](https://github.com/AtomiCloud/nix-registry/commit/f47a429e0ac7ed2adf7082e9a473879fb47d2be6))

## [2.3.0](https://github.com/AtomiCloud/nix-registry/compare/v2.2.0...v2.3.0) (2025-04-23)


### ✨ New Packages ✨

* **dotnetlint:** recursively run dotnet lint ([f3fc876](https://github.com/AtomiCloud/nix-registry/commit/f3fc876bf6b14f98d403a99bcdd2fd1ccdd7b08c))
* **helmlint:** recursively run helm lint ([bb5ca77](https://github.com/AtomiCloud/nix-registry/commit/bb5ca77a4c92a59c844c11a8eda3ea0f9296dab5))

## [2.2.0](https://github.com/AtomiCloud/nix-registry/compare/v2.1.0...v2.2.0) (2025-04-13)


### ✨ New Packages ✨

* **codecov:** Codecov CLI tool for code coverage reporting ([40f3985](https://github.com/AtomiCloud/nix-registry/commit/40f398523f167c0330c6b949b785a9931d3e139a))

## [2.1.0](https://github.com/AtomiCloud/nix-registry/compare/v2.0.1...v2.1.0) (2025-02-15)


### ⬆️ Packages Updated ⬆️

* **default:** bump for all atomi updates ([e0c3e11](https://github.com/AtomiCloud/nix-registry/commit/e0c3e1137f7a769302ecf541e0a26625d42b3aed))
* **infrauitls:** to include skopeo ([86f11e2](https://github.com/AtomiCloud/nix-registry/commit/86f11e2b08ba43ebf546fb1d9fc462052123bba3))
* **atomiutils:** update atomiutils to use include toybox ([e555f87](https://github.com/AtomiCloud/nix-registry/commit/e555f87851050ae9e3deda96d7609fe4ef0d41d3))

## [2.0.1](https://github.com/AtomiCloud/nix-registry/compare/v2.0.0...v2.0.1) (2025-02-14)


### 🐛 Bug Fixes 🐛

* **infralint:** incorrect moving binary in install script ([e056b31](https://github.com/AtomiCloud/nix-registry/commit/e056b31e97d34b088e6cd8b067140eb9abb699a7))


### ⬆️ Packages Updated ⬆️

* **atomi_release:** add patch, minor, and major version update types ([5bbf7e4](https://github.com/AtomiCloud/nix-registry/commit/5bbf7e4dde01889f61a5387e376161d3adda55b9))
* **infrautils:** include opentofu ([c4a152f](https://github.com/AtomiCloud/nix-registry/commit/c4a152f1845f25b1f384f93a3a3ed53e29db337e))
* **atomiutils:** include tar, awk, gomplate ([e9218da](https://github.com/AtomiCloud/nix-registry/commit/e9218da74ffe00e1eda590b1a62c388a47aee329))
* **infralint:** include terraform tools ([69942b0](https://github.com/AtomiCloud/nix-registry/commit/69942b0c8f52bf24d5aa84f76e52c5b7aec2c318))

## [2.0.0](https://github.com/AtomiCloud/nix-registry/compare/v1.2.0...v2.0.0) (2025-02-02)


### ✨ New Packages ✨

* **infra:** infralint and infrautils, bundling of infra related tools ([0dd96d5](https://github.com/AtomiCloud/nix-registry/commit/0dd96d58808c6f68d2b2e5cb103c99f4d882e5d2))


### ⬆️ Packages Updated ⬆️

* **sg:** update sg to 0.11.0 ([c2097c8](https://github.com/AtomiCloud/nix-registry/commit/c2097c8f683c5c886f49b3f34b122d58b3418996))

## [1.2.0](https://github.com/AtomiCloud/nix-registry/compare/v1.1.0...v1.2.0) (2025-01-29)


### ✨ New Packages ✨

* **garden:** Automation for Kubernetes development and testing ([1674072](https://github.com/AtomiCloud/nix-registry/commit/1674072ca9c98e2be23efb32da13d9a4a9fc4df8))

## [1.1.0](https://github.com/AtomiCloud/nix-registry/compare/v1.0.5...v1.1.0) (2025-01-29)


### ✨ New Packages ✨

* **atomiutils:** extend version of coreutils for atomicloud ([0df3c5a](https://github.com/AtomiCloud/nix-registry/commit/0df3c5a5f15f11f5f9b967db783b9bea018cb001))

## [1.0.5](https://github.com/AtomiCloud/nix-registry/compare/v1.0.4...v1.0.5) (2025-01-28)


### 🐛 Bug Fixes 🐛

* **drv:** include cyanprint back into registry ([7aea611](https://github.com/AtomiCloud/nix-registry/commit/7aea611df762f100156532eeade6d3f699deaf1e))

## [1.0.4](https://github.com/AtomiCloud/nix-registry/compare/v1.0.3...v1.0.4) (2025-01-28)


### 🐛 Bug Fixes 🐛

* **drv:** temp rm cyanprint to remove ref to old atomi ([b2e7f50](https://github.com/AtomiCloud/nix-registry/commit/b2e7f5026b4a83ad04ffb6b3332071229847a6b7))

## [1.0.3](https://github.com/AtomiCloud/nix-registry/compare/v1.0.2...v1.0.3) (2025-01-22)


### 🐛 Bug Fixes 🐛

* **drv:** self reference to remove dependency on old atomi ([1cb3ab0](https://github.com/AtomiCloud/nix-registry/commit/1cb3ab060a9be7ed6a4a665aec0ab05ea9329d20))

## [1.0.2](https://github.com/AtomiCloud/nix-registry/compare/v1.0.1...v1.0.2) (2025-01-09)


### 🐛 Bug Fixes 🐛

* check pkgs ([9d0be4a](https://github.com/AtomiCloud/nix-registry/commit/9d0be4a230b5153a288f817543383e01475bc24a))
* ci failing ([aee3a40](https://github.com/AtomiCloud/nix-registry/commit/aee3a409eb88f0a17350e826ce818d9614ef0fde))
* improve CI to check build ([56ece41](https://github.com/AtomiCloud/nix-registry/commit/56ece417b5f1f53b6fe5fdfacd8d6e30c9fc29cd))

## [1.0.1](https://github.com/AtomiCloud/nix-registry/compare/v1.0.0...v1.0.1) (2025-01-09)


### 🐛 Bug Fixes 🐛

* sg incorrect binding ([1db974d](https://github.com/AtomiCloud/nix-registry/commit/1db974d33ee410c6916befbb1dfd56e312bc6a83))

## 1.0.0 (2025-01-04)


### 🐛 Bug Fixes 🐛

* prettier styling ([cf43f73](https://github.com/AtomiCloud/nix-registry/commit/cf43f73b71b8fa213fff11de2976efa15f75edc4))


### ✨ New Packages ✨

* initial commit ([cbaaa38](https://github.com/AtomiCloud/nix-registry/commit/cbaaa38b3e596bd6fd92ed5d05b45a39ed3f8ba6))
