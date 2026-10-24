# Deployment Update Policy

This policy covers ovpn-go Docker images and self-contained Linux archives. For native commands and service management, see the [native Linux guide](v4/native-linux.md).

## Version model

Project release, OpenVPN runtime and persistent data schema are separate version axes:

- `IMAGE_VERSION` identifies the project release for both Docker and native distributions.
- `DATA_SCHEMA` identifies the persistent structured-state format.
- `OPENVPN_VERSION` identifies the bundled OpenVPN Community Edition runtime.
- `GO_RUNTIME_VERSION` is the application version reported by the Go binaries, derived from `IMAGE_VERSION`; it is not the Go SDK version.

`ovpn version --json` reports the project version, schema, source information and Go SDK version. `ovpn runtime capabilities --json` reports the OpenVPN compatibility contract. A candidate-version range is an automation constraint, not a compatibility promise.

## Delivery boundary

Project programs, templates, migration support and compatibility contracts are shipped together in a Docker image or native archive. Native archives additionally include the private userspace runtime and dependencies. The running VPN service does not download or update its own code. The separately invoked native installer/updater downloads and verifies published Release assets.

Updates must not silently apply changed YAML, rewrite credentials or migrate schemas merely because a service starts. On an initialized instance, explicit `server run` uses the applied SQLite configuration; applying desired YAML remains an operator action.

## Choose a release

- GHCR: `ghcr.io/yjrszcq/ovpn-go`; numeric tags `X.Y.Z`, `X.Y` and `X` track the project release. `latest` and verified `ovpn<version>` aliases are also published.
- Docker Hub: `szcq/openvpn`; numeric tags track the embedded OpenVPN version, and `latest` follows the stable image.
- Native archives: `openvpn-server-X.Y.Z-linux-amd64.tar.gz` and `openvpn-server-X.Y.Z-linux-arm64.tar.gz`, with `SHA256SUMS`. Choose the host architecture and verify the checksum.

Pin a concrete version in production. After a repository rename, update GHCR references to the current repository namespace; Docker Hub names are unchanged. A native Release is ready for installation only when both architecture archives and `SHA256SUMS` are present.

## Same-schema updates

Releases sharing a data schema interpret that schema identically. Back up data and configuration before updating. Do not run `migrate apply` for a same-schema change.

For Docker, stop the service, select the new image for both live and maintenance services, pull it, run read-only `state doctor`, and recreate the live service. Keep existing volumes and verify version, state and runtime health; see the [operations guide](v4/operations.md#image-update-without-a-schema-change).

For installer-managed native deployments, use the [online updater](v4/native-linux.md#update-and-recover). It preserves the original running/stopped state and service enablement, snapshots state after stopping the service, and checks health after restarting. Failed replacement or health checks restore the previous program and snapshot. Rerun the updater to recover an interrupted transaction. Custom installations must pass the same `OVPN_INSTALL_DIR`; managed recovery snapshots cover the installation's `config/`, `data/` and `run/`. Stop manually launched instances before updating.

The native updater accepts compatible schemas only and refuses downgrades. It is not a Docker-to-native conversion tool.

## Schema changes and rollback

A newer schema requires an explicit supported migration with the service stopped; see the [data schema upgrade policy](data-schema-upgrade-policy.md). The native online updater does not perform cross-schema migration.

A same-schema Docker rollback can recreate the service with the prior image. A successful native update cannot be downgraded using the online updater; for deliberate rollback, stop the service and restore a matching prior program and complete backup. Automatic failure rollback is separate from an operator-requested downgrade.

After schema migration, changing only the image or executable is insufficient: restore the complete pre-migration snapshot and use a program supporting that restored schema. Never restore only the SQLite file or merge a backup into live state.

## Release validation

Release validation covers initialization, PKI, client and address management, configuration, backup/recovery, migration compatibility and real VPN traffic. Native attachments are uploaded only after both native architecture build and acceptance jobs pass. Read each Release's compatibility and validation notes for the tested distributions and any hardware/emulation boundary.
