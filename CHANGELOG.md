# Changelog

## [0.5.0](https://github.com/jamestelfer/imds-broker/compare/v0.4.0...v0.5.0) (2026-06-30)


### Features

* add goreleaser binary releases and version command ([146b071](https://github.com/jamestelfer/imds-broker/commit/146b07188cdcf6ff43771deebd7974c2bd9f9e40))
* add goreleaser binary releases and version command ([89c0138](https://github.com/jamestelfer/imds-broker/commit/89c01386f4579a3111d41e841acd5cba9ff84e1d))
* **broker:** implement multi-server broker with Docker gateway discovery ([c1c014e](https://github.com/jamestelfer/imds-broker/commit/c1c014e820c08f6865d3595a99e75937e937c82f))
* **cmd:** add stderr text logging to serve command ([52e954c](https://github.com/jamestelfer/imds-broker/commit/52e954cc5125d723ac3925334b5450d2082b09f8))
* **cmd:** add stderr text logging to serve command ([81e5ab5](https://github.com/jamestelfer/imds-broker/commit/81e5ab53215f92759f3d2d9b6ea4e539bc1e6352))
* **cmd:** write rotating log files via lumberjack ([f1b0a26](https://github.com/jamestelfer/imds-broker/commit/f1b0a266599dea04305ce4c0a70f6595d7ef902c))
* **cmd:** write rotating log files via lumberjack ([3e725da](https://github.com/jamestelfer/imds-broker/commit/3e725daaf0ca4fa56667b9daa21503c46d5784f1))
* host-side broker configuration and diagnostics ([#28](https://github.com/jamestelfer/imds-broker/issues/28)) ([ee3baf4](https://github.com/jamestelfer/imds-broker/commit/ee3baf4f8eccbf239a01883a1494ca849cb4cd3f))
* **imdsserver:** add availability-zone endpoint for Python SDK region resolution ([74bb91c](https://github.com/jamestelfer/imds-broker/commit/74bb91cbe5968cdc701deef483b73a72cc291d3f))
* **imdsserver:** add Cached[T] generic TTL cache ([9a3abff](https://github.com/jamestelfer/imds-broker/commit/9a3abff8ad9fde1b44217f08420ca5b6f385cee2))
* **imdsserver:** add connection filter allow-list helpers ([8462782](https://github.com/jamestelfer/imds-broker/commit/84627829142fa8d30f6252a720d373c9b0d933ff))
* **imdsserver:** add instance identity document endpoint for Go/Java SDK region resolution ([81e3f29](https://github.com/jamestelfer/imds-broker/commit/81e3f29506fe2448e8872a4996821541569d15e2))
* **imdsserver:** add missing IMDS routes for SDK region resolution ([77a1ad9](https://github.com/jamestelfer/imds-broker/commit/77a1ad998f5d9decc5b1d8a30aaa5a44c9abd1d0))
* **imdsserver:** wire local-only connection filter into all listeners ([f807150](https://github.com/jamestelfer/imds-broker/commit/f80715017eee0123572588a5eca35822ec33c0f7))
* implement Phase 1 project scaffold and pkg/imdsserver ([4b58b43](https://github.com/jamestelfer/imds-broker/commit/4b58b4301442f0987f18bb0e215c6928db331a8c))
* **mcpserver:** add MCP server with list_profiles, create_server, stop_server tools ([ed8882f](https://github.com/jamestelfer/imds-broker/commit/ed8882f454240f9e128dc879722a344c046d9154))
* **mcpserver:** include port in create_server response ([9b46c98](https://github.com/jamestelfer/imds-broker/commit/9b46c985056ec3fdfb4e3c27a2c6b4b396f15e54))
* **mcpserver:** include port in create_server response ([523589e](https://github.com/jamestelfer/imds-broker/commit/523589e3ec693720e653330dafd87e5bbfd33d10))
* **profiles:** add profile lister and profiles CLI command ([c654b01](https://github.com/jamestelfer/imds-broker/commit/c654b01577c3f1fa90e7496ea1789d49af9b0ef5))
* **profiles:** return structured Profile objects with account ID and region ([559ca46](https://github.com/jamestelfer/imds-broker/commit/559ca46ff3a3d74d1a6234caa110f64527481556))
* **profiles:** return structured Profile objects with account ID and region ([efa8117](https://github.com/jamestelfer/imds-broker/commit/efa8117b1e056c4259bb386333afe0a11e1df086))
* **release:** add homebrew cask publishing via goreleaser ([4437512](https://github.com/jamestelfer/imds-broker/commit/4437512cf70001ea646feffb7644a8cf24d81fc2))
* **release:** add npm multi-arch publish script ([eade577](https://github.com/jamestelfer/imds-broker/commit/eade57783cdf4f5630d5ba3a38a611557cba94f5))
* **serve:** implement serve command with real AWS credential chain ([98d7480](https://github.com/jamestelfer/imds-broker/commit/98d7480cef6523c9a884b5afdb817607206b0663))


### Bug Fixes

* **broker:** bind to 0.0.0.0 for Docker reachability, log client IP ([658f374](https://github.com/jamestelfer/imds-broker/commit/658f3749a2a65feae301735806502e14b92f7085))
* **broker:** bind to 0.0.0.0 for Docker reachability, log client IP ([943fbd5](https://github.com/jamestelfer/imds-broker/commit/943fbd5aa8418ebc47c3f9bc364f721854da5d87))
* **cmd:** serve binds to 0.0.0.0 for container reachability ([08dff04](https://github.com/jamestelfer/imds-broker/commit/08dff0477b2a7e28477c890bc27fcbb271975bcc))
* **imdsserver:** detach credential retrieval from request context ([70f5542](https://github.com/jamestelfer/imds-broker/commit/70f554283e1d2d82133ff570e3083e46e45258e5))
* **mcpserver:** enforce profile filter in create_server via ProfileFilter interface ([2a2fd8e](https://github.com/jamestelfer/imds-broker/commit/2a2fd8eea4e0a7ca9b61fb24871fe8afe67cb0b5))
* missed format and agent change ([e978ca0](https://github.com/jamestelfer/imds-broker/commit/e978ca03c18de76a40168307edb5bec3f41c19a2))
* **release:** align goreleaser build with just build target ([68fdf87](https://github.com/jamestelfer/imds-broker/commit/68fdf8736991eda00b32ad5eec89be00bb4f253a))
* **release:** include README in npm main package ([e3257cf](https://github.com/jamestelfer/imds-broker/commit/e3257cfefe652f3b7bdf270ed5f6173cfe8db982))

## [0.4.0](https://github.com/jamestelfer/imds-broker/compare/v0.3.0...v0.4.0) (2026-06-30)


### Features

* host-side broker configuration and diagnostics ([#28](https://github.com/jamestelfer/imds-broker/issues/28)) ([ee3baf4](https://github.com/jamestelfer/imds-broker/commit/ee3baf4f8eccbf239a01883a1494ca849cb4cd3f))


### Bug Fixes

* **release:** include README in npm main package ([e3257cf](https://github.com/jamestelfer/imds-broker/commit/e3257cfefe652f3b7bdf270ed5f6173cfe8db982))
