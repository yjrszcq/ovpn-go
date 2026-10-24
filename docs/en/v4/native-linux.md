# Native Linux installation

Linux archives include `ovpn`, OpenVPN, OpenSSL, Easy-RSA, the musl loader, shared libraries, providers, network tools, shell and editor. These components do not need to be installed on the host. Docker remains available with its existing configuration and commands.

Use `linux-amd64` for x86-64 and `linux-arm64` for AArch64. The host needs Linux TUN support, network administration privileges and a reachable OpenVPN port. Configure host/cloud firewalls yourself; `ovpn` manages only its instance rules and does not disable SELinux or firewalld. IPv4 tunnels support UDP/TCP transport over IPv4 or IPv6; IPv6 tunnel addressing is not supported.

Compatibility covers Debian 13.7, Ubuntu 26.10, Rocky Linux 10.2, Alpine 3.24.3 and current Arch Linux. AArch64 Arch compatibility refers separately to Arch Linux ARM. Local AArch64 verification uses emulation; the Release workflow additionally requires native AArch64 acceptance before uploading attachments.

## Online installation

Wait until the Release page contains both architecture archives and `SHA256SUMS`. The bootstrap requires Bash, curl, tar, a `sha256sum` tool and standard Linux command-line utilities. It does not install host packages.

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/ovpn-go/main/install.sh')"
```

The installer first asks for a language (English by default), then Install, Update or Uninstall (Install by default). The default directory is `/opt/ovpn` and the default version is the latest stable Release. Press Enter to accept displayed defaults. Your public hostname/IP is required. Further prompts cover the VPN network, transport, port, NAT, DNS, REST API settings and optional advanced settings.

It detects the host architecture, verifies the archive, creates `/usr/local/bin/ovpn` and registers a systemd or OpenRC service. Configuration is validated and written once after confirmation. It then asks whether to enable boot startup and start now (both default to yes). Unrelated existing commands, directories and service definitions are refused. One managed service is supported per host.

Ctrl-C interrupts setup. Rerun and select the same action to retain completed answers and resume at the unfinished question. Incomplete configuration is never activated; reinstalling retains existing configuration. Installation and update settings are selected interactively without environment variables. Choose a non-overlapping network and allow the VPN port through host/cloud firewalls.

## Configure and start

Interactive installation already handles configuration and startup choices. Use these commands for later manual edits, startup, or after choosing not to start during installation.

```bash
sudo ovpn config edit
```

Set `server.endpoint` to your public hostname/IP and choose a non-overlapping `ipv4.network`. Review transport, NAT, routes and DNS settings before starting. The bundled editor is also available as `/opt/ovpn/bin/nano`.

For systemd:

```bash
sudo systemctl enable --now ovpn
sudo systemctl status ovpn
```

For OpenRC:

```bash
sudo rc-update add ovpn default
sudo rc-service ovpn start
```

For foreground operation:

```bash
sudo /opt/ovpn/ovpn
```

No arguments starts the service and initializes empty data using the existing configuration. Missing configuration produces instructions and stops. Do not run a foreground instance alongside the managed service. SIGTERM or Ctrl-C stops child processes and cleans instance network rules.

## Files and everyday commands

| Path relative to the extracted directory | Purpose |
| --- | --- |
| `ovpn` | Relocatable CLI and foreground entrypoint |
| `runtime/`, `bin/` | Bundled programs, libraries, resources and licenses |
| `config/config.yaml` | Your configuration |
| `data/` | SQLite, PKI, profiles, generated configuration and audit state |
| `logs/` | Link to `data/logs/`, including rotated OpenVPN logs |
| `run/` | Runtime sockets and service diagnostic output |

```bash
sudo ovpn runtime health
sudo ovpn client create laptop --ipv4 auto
sudo ovpn client export laptop --output /root/laptop.ovpn
sudo ovpn state doctor
sudo ovpn config plan
sudo ovpn config apply --yes
```

The CLI and optional REST API retain their existing interfaces and `OVPN_*` overrides. Overrides apply to each process; use the same settings for the service and CLI. For installer-managed updates, keep writable state under the installation's `config/`, `data/` and `run/` directories so it is included in recovery snapshots. Use `journalctl -u ovpn` for service startup diagnostics; OpenRC writes them under `run/service*.log`. The [REST API guide](rest-api.md) documents enabling and securing the API.

## API and offline maintenance

The API is disabled by default. Configure it in the installer or run `sudo ovpn config edit -a`, edit the following YAML and confirm application:

```yaml
api:
  enabled: true
  listenIP: 127.0.0.1
  port: 11940
  cors: []
```

An empty `cors` list disables CORS. Use an HTTPS reverse proxy for remote access; see the [REST API guide](rest-api.md).

Create an API key for this installation:

```bash
sudo /opt/ovpn/ovpn api key create frontend-production --output /root/frontend.key
```

For offline repair, configuration or migration operations, stop the service first. Commands requiring maintenance authorization use this form:

```bash
sudo env OVPN_MAINTENANCE=true /opt/ovpn/ovpn migrate plan
```

`migrate plan` is read-only; do not run `migrate apply` for a same-schema update. See the [command reference](commands.md) for supported migration sources and confirmation requirements.

## Update and recover

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/ovpn-go/main/install.sh')"
```

Rerun the script and select Update. The updater downloads and verifies first, then locks the installation. A running service is stopped, snapshotted, updated and restarted with a health check; a stopped service stays stopped. Your configuration, certificates, database, profiles, logs, permissions and service enablement are retained. A failed replacement or health check restores the previous program and snapshot. After an interrupted update, rerun the updater to recover the transaction. A retained `.update-backup/` means recovery needs attention; preserve it until the instance is healthy.

Confirm the installation directory and select the latest stable release or enter a published version in the prompts.

Updates require the same data schema and refuse downgrades. Repeating installation or updating to the installed version preserves state. Stop manually launched instances before updating; the updater refuses to terminate them. Keep independent backups as described in the [operations guide](operations.md).

## Uninstall

Run the same installer command and select Uninstall. By default, configuration, certificates, database and logs are retained. The service is stopped and the program, service definition and command link are removed. Reinstalling in the same directory reuses retained data. Permanent deletion requires choosing not to keep data, typing `DELETE` and separately confirming uninstall. Back up first; deleted data cannot be recovered by the installer.

## Offline backup and restore

Examples below use the default installation and systemd. For OpenRC, replace `systemctl stop/start ovpn` with `rc-service ovpn stop/start`. For foreground operation, stop the process and restart it manually after the operation. Stop all writers, including maintenance commands, before copying data.

```bash
sudo systemctl stop ovpn
sudo /opt/ovpn/bin/tar -czf /root/ovpn-backup.tar.gz -C /opt/ovpn config data
sudo chmod 600 /root/ovpn-backup.tar.gz
sudo systemctl start ovpn
```

Store backups encrypted: they contain SQLite, CA/server/client private keys, certificates and profiles. A data directory and its configuration must be backed up and restored together.

To restore, keep the service stopped, extract a trusted backup into a fresh directory and replace the complete data and configuration directories:

```bash
sudo systemctl stop ovpn
sudo mkdir -m 700 /opt/ovpn/restore-work
sudo /opt/ovpn/bin/tar -xzf /root/ovpn-backup.tar.gz -C /opt/ovpn/restore-work
sudo mv /opt/ovpn/data /opt/ovpn/data-before-restore
sudo mv /opt/ovpn/config /opt/ovpn/config-before-restore
sudo mv /opt/ovpn/restore-work/data /opt/ovpn/data
sudo mv /opt/ovpn/restore-work/config /opt/ovpn/config
sudo /opt/ovpn/ovpn state doctor
sudo systemctl start ovpn
sudo /opt/ovpn/ovpn runtime health
```

Use an installation supporting the backup's schema. Do not restore only `state.db` or merge into existing data. Keep the previous directories until state and a real client connection are verified. If the extraction path differs from the backup's original installation, run `sudo /opt/ovpn/ovpn repair apply --yes` after restoring and before starting to rebuild derived absolute paths.

## Manual extraction and relocation

Download the matching archive and `SHA256SUMS` from the same Release. Replace `X.Y.Z` and the architecture as needed:

```bash
awk '$2 == "openvpn-server-X.Y.Z-linux-amd64.tar.gz" {print}' SHA256SUMS | sha256sum -c -
tar -xzf openvpn-server-X.Y.Z-linux-amd64.tar.gz
cd openvpn-server
cp config/config.example.yaml config/config.yaml
# Edit config/config.yaml before starting.
sudo ./ovpn
```

No service manager is required for manual foreground operation. To use the CLI through PATH, add the extraction root, for example `export PATH="/opt/ovpn:$PATH"`; keep the whole directory together. Absolute paths, symlinks, different working directories and paths containing spaces are supported. `sudo` may reset PATH; use the absolute executable path or the installer-created `/usr/local/bin/ovpn` link in that case.

Before initialization, move the directory freely. For an initialized instance, stop it, move the complete directory, then run `sudo /new/path/ovpn repair apply --yes` to regenerate derived absolute paths before restarting. Update any service definition and command symlink that still points to the old location. The installer-managed updater targets the directory and service originally registered; manual relocations require adjusting those paths first.

### Preview installation

From the repository, run `bash install.sh --dry-run` without sudo. It retains language, action and configuration prompts and shows the draft and intended operations, without downloading, writing installation/configuration/resume state or changing services. It does not verify Release assets or runtime health, and answers are discarded on exit. Select Update or Uninstall in the same preview menu.
