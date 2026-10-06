# ESF 0.6.1 upgrade candidate

0.6.1 updates the supported agent runners and dependency pins and fixes public
internet denial when creating sandboxes through CubeAPI 0.7.2. Unreal is 0.3.1,
OpenCode V2 is 2.0.24, and the opt-in Pi Durable runner is 1.0.4. Temporal UI is
2.55.0; the current Temporal server, SDK and Machinist revision are retained.

The [upgrade record](../release/qualification/component-upgrade-2026-10-06.md)
records component versions, local artifacts, tests, model-backed factory runs,
the callback-routing repair and remaining qualification boundaries. Python,
Go, npm lockfiles, embedded frontend assets, runtime checksums and GitHub
Actions pins were updated together.

Release metadata is synchronized across factory and Machinist builds, Python
project/lock metadata, Helm and Docker image defaults, and both release
inventories. Historical 0.6.0 evidence is retained unchanged.

The [existing qualification procedure](release-v0.6.0.md) applies using the
0.6.1 source and corresponding artifact/tag names. A fresh companion manifest
must match the exact 0.6.1 source and assets. Production gates remain pending;
the local test record is functional development evidence. A source version
bump does not publish release archives or establish production qualification.
