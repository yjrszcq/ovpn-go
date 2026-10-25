#!/usr/bin/env bash
# Runs after systemd/OpenRC boots as PID 1 inside an isolated VM.
set -Eeuo pipefail
trap 'echo "VM assertion failed: ${BASH_SOURCE[0]}:$LINENO: $BASH_COMMAND"' ERR
exec > >(tee /dev/console) 2>&1
finish() {
  code=$?
  trap - EXIT
  if [ "$code" = 0 ]; then echo OVPN_VM_TEST_PASS; else
    echo "OVPN_VM_TEST_FAIL=$code"
    if [ -d /run/systemd/system ]; then journalctl -b --no-pager -u ovpn -u dbus -u dbus-broker -n 80 || true; fi
    for instance in /opt/ovpn '/opt/interactive vpn' "$(cat /fixtures/install-dir)"; do
      for diagnostic in "$instance/run/service.log" "$instance/run/service-error.log" "$instance/data/logs/openvpn.log"; do
        if [ -f "$diagnostic" ]; then
          printf '%s\n' "--- $diagnostic ---"
          tail -n 80 "$diagnostic" || true
        fi
      done
    done
    for diagnostic in /run/fixture-http.log /run/interrupted.log /run/unmanaged.log /run/dns-client.log; do
      if [ -f "$diagnostic" ]; then
        printf '%s\n' "--- $diagnostic ---"
        tail -n 80 "$diagnostic" || true
      fi
    done
  fi
  sync
  if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then systemctl poweroff --no-block; else poweroff; fi
}
trap finish EXIT
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
cat /etc/os-release
# The minimal ARM initramfs can leave devtmpfs memory devices at 0660.
# Restore their normal modes before starting services that drop privileges.
chmod 666 /dev/null /dev/zero /dev/full /dev/random /dev/urandom
ls -ld /dev /dev/null /opt
if [ -d /run/systemd/system ]; then
  systemctl start dbus.service
  systemctl is-active --quiet dbus.service
fi
modprobe tun
mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200
if [ -f /fixtures/rebooted ]; then
  for _ in $(seq 1 60); do
    if /usr/local/bin/ovpn runtime health; then break; fi
    sleep 1
  done
  /usr/local/bin/ovpn runtime health
  test "$(/usr/local/bin/ovpn version --short)" = "$(cat /fixtures/new-version)"
  exit 0
fi
# Serve actual release assets inside the guest; no GitHub write or mocked service.
python3 -m http.server 18080 --bind 127.0.0.1 --directory /fixtures/releases >/run/fixture-http.log 2>&1 &
for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18080/SHA256SUMS >/dev/null; then break; fi
  sleep 1
done
python3 /fixtures/installer-interactive.py
run_installer() {
  local installer=(bash /fixtures/install.sh --non-interactive --action "${OVPN_INSTALL_ACTION:-install}"
    --directory "${OVPN_INSTALL_DIR:-/opt/ovpn}" --version "${OVPN_VERSION:-latest}"
    --download-base-url "${OVPN_DOWNLOAD_BASE_URL:-http://127.0.0.1:18080}"
    --command-link "${OVPN_COMMAND_LINK:-/usr/local/bin/ovpn}")
  if [ "${1:-}" = --exec ]; then exec "${installer[@]}"; else "${installer[@]}"; fi
}
export OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080
export OVPN_VERSION
export OVPN_INSTALL_DIR
OVPN_INSTALL_DIR="$(cat /fixtures/install-dir)"
for scenario in corrupt missing unsafe wrong-arch wrong-schema unlisted; do
  if OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_DIR=/opt/ovpn-negative OVPN_COMMAND_LINK=/usr/local/bin/ovpn-negative OVPN_DOWNLOAD_BASE_URL="http://127.0.0.1:18080/$scenario" run_installer >/run/negative.log 2>&1; then
    echo "invalid asset accepted: $scenario"
    exit 1
  fi
  test ! -e /opt/ovpn-negative
  test ! -e /usr/local/bin/ovpn-negative
  test ! -e /tmp/escaped
  echo "asset rejection passed: $scenario"
done
OVPN_VERSION="$(cat /fixtures/old-version)"
if [ -d /run/systemd/system ]; then unit=/etc/systemd/system/ovpn.service; else unit=/etc/init.d/ovpn; fi
ln -s /nonexistent/ovpn-foreign "$unit"
if run_installer >/run/unit-conflict.log 2>&1; then exit 1; fi
grep -Fq 'must not be replaced' /run/unit-conflict.log
test -L "$unit"
test ! -e "$OVPN_INSTALL_DIR"
rm "$unit"
run_installer
test -L /usr/local/bin/ovpn
if [ "$OVPN_INSTALL_DIR" != /opt/ovpn ]; then
  ln -s "$OVPN_INSTALL_DIR" /opt/ovpn
  ln -s "$OVPN_INSTALL_DIR.install-lock" /opt/ovpn.install-lock
fi
run_installer
cp /opt/ovpn/config/config.example.yaml /opt/ovpn/config/config.yaml
sed -i 's/vpn.example.com/127.0.0.1/' /opt/ovpn/config/config.yaml
service_action() {
  if [ -d /run/systemd/system ]; then systemctl "$1" ovpn; else rc-service ovpn "$1"; fi
}
if service_action status; then
  echo 'first install unexpectedly started service'
  exit 1
fi
if [ -d /run/systemd/system ]; then
  if systemctl is-enabled ovpn; then
    echo 'first install unexpectedly enabled service'
    exit 1
  fi
else
  test ! -e /etc/runlevels/default/ovpn
fi
. /fixtures/service-entrypoint.sh
# Existing command conflicts must leave both the command and target intact.
printf 'existing command\n' >/usr/local/bin/ovpn-conflict
if OVPN_INSTALL_DIR=/opt/ovpn-conflict OVPN_COMMAND_LINK=/usr/local/bin/ovpn-conflict run_installer >/run/conflict.log 2>&1; then exit 1; fi
grep -Fq 'existing command' /usr/local/bin/ovpn-conflict
test ! -e /opt/ovpn-conflict
if [ -d /run/systemd/system ]; then systemctl enable ovpn; else rc-update add ovpn default; fi
service_action start
for _ in $(seq 1 60); do
  if ovpn runtime health; then break; fi
  sleep 1
done
ovpn runtime health
ovpn client create vm-client --ipv4 auto --output /opt/ovpn/config/vm-client.ovpn
# Exercise OpenVPN's default trusted DNS helper, without raising script-security.
# The VM resolver is a normal file, unlike Docker's injected bind mount.
cp /etc/resolv.conf /run/resolv-before
"$OVPN_INSTALL_DIR/bin/openvpn" --config /opt/ovpn/config/vm-client.ovpn --dev tun-dns-probe --dns server 0 address 1.1.1.1 >/run/dns-client.log 2>&1 &
dns_client=$!
for _ in $(seq 1 60); do
  if grep -Fq 'Initialization Sequence Completed' /run/dns-client.log; then break; fi
  sleep 0.2
done
if ! grep -Fq 'Initialization Sequence Completed' /run/dns-client.log; then
  cat /run/dns-client.log
  exit 1
fi
grep -Fq 'nameserver 1.1.1.1' /etc/resolv.conf
kill -TERM "$dns_client"
wait "$dns_client"
grep -Fq 'dns up command exited with status 0' /run/dns-client.log
grep -Fq 'dns down command exited with status 0' /run/dns-client.log
cmp /run/resolv-before /etc/resolv.conf
echo 'bundled DNS helper configuration and cleanup passed'
certificate="$(sha256sum /opt/ovpn/data/pki/ca.crt)"
chmod 640 /opt/ovpn/config/vm-client.ovpn
profile="$(sha256sum /opt/ovpn/config/vm-client.ovpn)"
service_action stop
# Existing maintenance readers/writers exclude replacement and preserve service state.
for original_state in stopped running; do
  if [ "$original_state" = running ]; then
    service_action start
    wait_healthy
  fi
  for coordination in .ovpn-runtime.lock .ovpn-data.lock; do
    exec 7<>"/opt/ovpn/data/$coordination"
    /opt/ovpn/bin/flock -s 7
    if OVPN_VERSION="$(cat /fixtures/middle-version)" OVPN_INSTALL_ACTION=update run_installer 7>&- >/run/maintenance-update.log 2>&1; then
      echo 'update overlapped a maintenance lock'
      exit 1
    fi
    grep -Fq 'maintenance is busy' /run/maintenance-update.log
    if [ -e /opt/ovpn/.update-backup ]; then
      cat /run/maintenance-update.log
      exit 1
    fi
    test "$(ovpn version --short)" = "$(cat /fixtures/old-version)"
    if [ "$original_state" = running ]; then wait_healthy; elif service_action status; then exit 1; fi
    exec 7>&-
  done
  if [ "$original_state" = running ]; then service_action stop; fi
done
legacy_unit
OVPN_VERSION="$(cat /fixtures/middle-version)" OVPN_INSTALL_ACTION=update run_installer
if service_action status; then
  echo 'stopped upgrade unexpectedly started service'
  exit 1
fi
test "$(ovpn version --short)" = "$(cat /fixtures/middle-version)"
cmp /run/installer-service "$unit"
service_action start
for _ in $(seq 1 60); do
  if ovpn runtime health; then break; fi
  sleep 1
done
# The lock must reject another live updater without stopping the service.
exec 9<>/opt/ovpn.install-lock
/opt/ovpn/bin/flock -n 9
if OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update run_installer >/run/concurrent.log 2>&1; then exit 1; fi
grep -Fq 'another installation/update is active' /run/concurrent.log
exec 9>&-
ovpn runtime health
legacy_unit
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update run_installer
ovpn runtime health
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
test "$(sha256sum /opt/ovpn/config/vm-client.ovpn)" = "$profile"
test "$(stat -c %a /opt/ovpn/config/vm-client.ovpn)" = 640
test "$(ovpn version --short)" = "$(cat /fixtures/new-version)"
service_action stop
ovpn server run >/run/unmanaged.log 2>&1 &
unmanaged=$!
for _ in $(seq 1 60); do
  if ovpn runtime health; then break; fi
  sleep 1
done
ovpn runtime health
if OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update run_installer >/run/unmanaged-update.log 2>&1; then exit 1; fi
grep -Fq 'unmanaged instance detected' /run/unmanaged-update.log
ovpn runtime health
kill -TERM "$unmanaged"
wait "$unmanaged"
service_action start
lock_paths=(/opt/ovpn/run/.server.lock /opt/ovpn/data/.ovpn-runtime.lock /opt/ovpn/data/.ovpn-data.lock)
lock_inodes="$(stat -c '%d:%i' "${lock_paths[@]}")"
printf '\n# rollback sentinel\n' >>"$unit"
cp -a "$unit" /run/pre-rollback-service
# A deliberately broken, checksum-valid candidate must trigger real rollback.
if OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update run_installer; then
  echo 'broken update unexpectedly succeeded' >&2
  exit 1
fi
ovpn runtime health
cmp /run/pre-rollback-service "$unit"
test "$(stat -c '%d:%i' "${lock_paths[@]}")" = "$lock_inodes"
cp -a /run/installer-service "$unit"
reload_unit
cp -a "$unit" /run/pre-rollback-service
test "$(ovpn version --short)" = "$(cat /fixtures/new-version)"
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
# Kill a real updater after replacement, before its failing health check ends.
OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update run_installer --exec >/run/interrupted.log 2>&1 &
updater=$!
# Observe the transaction directly: repeated CLI launches can miss the health-check
# window on slow VM runners. The journal and manifest identify replacement without
# launching another runtime or competing with the updater for its locks.
python3 - "$updater" <<'PYTHON'
import json
import os
from pathlib import Path
import sys
import time

updater = int(sys.argv[1])
expected = Path('/fixtures/bad-version').read_text().strip()
deadline = time.monotonic() + 180
while time.monotonic() < deadline:
    try:
        metadata = json.loads(Path('/opt/ovpn/runtime/manifest.json').read_text())
        if (Path('/opt/ovpn/.update-backup/ready').is_file()
                and metadata['version'] == expected
                and not any(Path(f'/proc/{updater}/fd/{fd}').exists() for fd in (6, 7, 8))):
            break
    except (FileNotFoundError, json.JSONDecodeError):
        pass
    try:
        os.kill(updater, 0)
    except ProcessLookupError:
        raise SystemExit('Updater exited before the interruption checkpoint')
    time.sleep(0.05)
else:
    raise SystemExit('Updater did not reach the interruption checkpoint within 180 seconds')
PYTHON
# Health checking releases maintenance locks to permit the managed service to start.
# Recovery must reacquire them before restoring, not replace their inodes.
kill -KILL "$updater"
wait "$updater" || true
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update run_installer
ovpn runtime health
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
test ! -e /opt/ovpn/.update-backup
cmp /run/pre-rollback-service "$unit"
test "$(stat -c '%d:%i' "${lock_paths[@]}")" = "$lock_inodes"
service_action stop
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update run_installer
if service_action status; then
  echo 'stopped service unexpectedly started' >&2
  exit 1
fi
# A second boot proves persistence and enabled-service startup.
if [ ! -f /fixtures/rebooted ]; then
  touch /fixtures/rebooted
  service_action start
  echo OVPN_VM_REBOOT
  sync
  trap - EXIT
  reboot
fi
