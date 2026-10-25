#!/usr/bin/env bash
# Sourced by vm-guest.sh inside an actual systemd/OpenRC VM.
: "${unit:?}" "${OVPN_INSTALL_DIR:?}"
wait_healthy() {
  local _
  for _ in $(seq 1 60); do
    if ovpn runtime health >/dev/null 2>&1; then return; fi
    sleep 1
  done
  ovpn runtime health
}
reload_unit() { if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi; }
legacy_unit() {
  sed -i 's/ server run$//; s/ --$//' "$unit"
  reload_unit
}
# Match the shipped examples to the installer, then actually register the example.
cp -a "$unit" /run/installer-service
if [ -d /run/systemd/system ]; then
  example=/fixtures/services/systemd/ovpn.service
else example=/fixtures/services/openrc/ovpn; fi
sed "s|/opt/ovpn|$OVPN_INSTALL_DIR|g" "$example" >/run/manual-service
cmp /run/manual-service "$unit"
cp /run/manual-service "$unit"
if [ ! -d /run/systemd/system ]; then chmod 755 "$unit"; fi
reload_unit
ovpn | grep -F 'Command tree:'
test ! -e /opt/ovpn/data/meta/state.db
# Editing before the first service start needs neither init nor apply.
sed -i 's/^    port: 1194$/    port: 1195/' /opt/ovpn/config/config.yaml
service_action start
wait_healthy
ovpn config show --json | grep -E '"revision": *1'
grep -E '^port 1195$' /opt/ovpn/data/server/server.conf
certificate_before="$(sha256sum /opt/ovpn/data/pki/ca.crt)"
if ovpn server run >/run/duplicate.log 2>&1; then
  echo 'managed instance duplicate accepted'
  exit 1
fi
ovpn runtime health
service_action restart
wait_healthy
service_action stop
ovpn server run >/run/foreground.log 2>&1 &
foreground=$!
wait_healthy
service_action start || true
sleep 2
kill -0 "$foreground"
service_action stop || true
ovpn runtime health
kill -TERM "$foreground"
wait "$foreground"
# Registration and unregistration preserve the instance and its certificates.
if [ -d /run/systemd/system ]; then
  systemctl enable ovpn
  systemctl disable ovpn
  journalctl -u ovpn --no-pager -n 10
else
  rc-update add ovpn default
  rc-update del ovpn default
  test -s /opt/ovpn/run/service.log
fi
rm "$unit"
reload_unit
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate_before"
cp -a /run/installer-service "$unit"
reload_unit
# Same-version update must repair the old no-argument service while stopped.
legacy_unit
OVPN_INSTALL_ACTION=update run_installer
cmp /run/installer-service "$unit"
if service_action status; then
  echo 'same-version repair started stopped service'
  exit 1
fi
# Reinstall/resume also repairs its service without replacing user configuration.
legacy_unit
run_installer
cmp /run/installer-service "$unit"
# Reject a service pointing at a different executable, including same-version update.
cp -a "$unit" /run/owned-service
printf '[Service]\nExecStart="/foreign/ovpn"\n' >"$unit"
if OVPN_INSTALL_ACTION=update run_installer >/run/foreign-service.log 2>&1; then exit 1; fi
grep -F 'does not belong' /run/foreign-service.log
cp -a /run/owned-service "$unit"
reload_unit
# Restore desired baseline through apply, never by merely restarting.
sed -i 's/^    port: 1195$/    port: 1194/' /opt/ovpn/config/config.yaml
ovpn config apply --yes
echo 'manual service examples, first-start edits, cross-mode locks and same-version migration: PASS'
