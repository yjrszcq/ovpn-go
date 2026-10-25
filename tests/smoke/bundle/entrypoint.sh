#!/usr/bin/env bash
# Runs entirely inside an isolated bundle container.
set -euo pipefail
unset OVPN_DATA_DIR OVPN_CONFIG_FILE OVPN_RUNTIME_DIR
cp -a /opt/ovpn /tmp/entrypoint-instance
root=/tmp/entrypoint-instance
export PATH="$root/bin:$PATH"
pid=''
cleanup() {
  if [ -n "$pid" ]; then
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" || true
  fi
}
trap cleanup EXIT
ovpn | grep -F 'Command tree:'
test ! -e "$root/data/meta/state.db"
if ovpn server run >/tmp/entrypoint.log 2>&1; then exit 1; fi
grep -F config.example.yaml /tmp/entrypoint.log
cp "$root/config/config.example.yaml" "$root/config/config.yaml"
sed -i 's/vpn.example.com/127.0.0.1/' "$root/config/config.yaml"
# Partial or corrupt data is never treated as a new instance.
mkdir -p "$root/data/meta"
printf 'corrupt database\n' >"$root/data/meta/state.db"
if ovpn server run >/tmp/entrypoint.log 2>&1; then exit 1; fi
grep -F 'corrupt database' "$root/data/meta/state.db"
rm -rf "$root/data"
ovpn server init
ovpn config show --json | grep -E '"revision": *1'
if ovpn runtime health >/dev/null 2>&1; then
  echo 'init started a service'
  exit 1
fi
sed -i 's/^    port: 1194$/    port: 1195/' "$root/config/config.yaml"
start() {
  ovpn server run >/tmp/entrypoint.log 2>&1 &
  pid=$!
  for _ in $(seq 1 60); do
    if ovpn runtime health >/dev/null 2>&1; then return; fi
    if ! kill -0 "$pid" 2>/dev/null; then
      cat /tmp/entrypoint.log
      return 1
    fi
    sleep 1
  done
  cat /tmp/entrypoint.log
  return 1
}
stop() {
  kill -TERM "$pid"
  wait "$pid"
  pid=''
}
start
grep -E '^port 1194$' "$root/data/server/server.conf"
if ovpn server run >/tmp/duplicate.log 2>&1; then
  echo 'duplicate start accepted'
  exit 1
fi
ovpn runtime health
stop
# Applying a change while stopped updates the next startup, not merely restarting.
ovpn config apply --yes
start
grep -E '^port 1195$' "$root/data/server/server.conf"
stop
# An edit before any initialization is consumed as revision 1 on first run.
rm -rf "$root/data" "$root/run"
start
grep -E '^port 1195$' "$root/data/server/server.conf"
ovpn config show --json | grep -E '"revision": *1'
stop
if iptables-save | grep -E 'OVPN_|ovpn:'; then
  echo 'rules remain'
  exit 1
fi
echo 'explicit entrypoint initialization, applied state, corrupt state and duplicate locking passed'
