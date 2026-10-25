#!/usr/bin/env bash
# Standalone bilingual installer; safe to execute with bash -c "$(curl -fsSL URL)".
set -euo pipefail
umask 077
language=en
action=install
interactive=1
dry_run=0
root=''
version=latest
base=''
link=/usr/local/bin/ovpn
state=''
install_locked=0
transaction=0
new_install=0
new_service=0
was_running=0
wizard_starting=0
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
staging=''
candidate=''
say() { if [ "$language" = zh ]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi; }
fail() {
  say "ovpn: $*" "ovpn：操作失败：$*" >&2
  exit 1
}
while [ "$#" -gt 0 ]; do
  case "$1" in
  --dry-run)
    dry_run=1
    shift
    ;;
  --non-interactive)
    interactive=0
    shift
    ;;
  --action | --directory | --version | --download-base-url | --command-link)
    [ "$#" -ge 2 ] || fail "missing value: $1"
    case "$1" in
    --action) action="$2" ;;
    --directory) root="$2" ;;
    --version) version="$2" ;;
    --download-base-url) base="$2" ;;
    --command-link) link="$2" ;;
    esac
    shift 2
    ;;
  *) fail "unknown argument: $1" ;;
  esac
done
[ "$dry_run" = 1 ] || [ "$(id -u)" = 0 ] || fail 'installation requires root'
if [ "$interactive" = 1 ] && [ ! -t 0 ]; then fail 'an interactive terminal is required (automation must explicitly use --non-interactive)'; fi
read_value() {
  local label="$1" default="$2"
  printf '%s' "$label"
  if [ -n "$default" ]; then printf ' [%s]' "$default"; fi
  printf ': '
  IFS= read -r REPLY || {
    if [ "$dry_run" = 1 ]; then
      say 'Preview interrupted; answers were discarded.' '试运行中断；答案不会保留。'
    else say 'Input interrupted; run the installer again to resume.' '输入中断；重新运行安装脚本即可继续。'; fi
    exit 130
  }
  REPLY="${REPLY:-$default}"
}
if [ "$interactive" = 1 ]; then
  while true; do
    read_value 'Language / 语言: 1) English  2) 中文' 1
    case "$REPLY" in 1 | en | English)
      language=en
      break
      ;;
    2 | zh | 中文)
      language=zh
      break
      ;;
    esac
  done
  while true; do
    read_value "$(say 'Action: 1) Install  2) Update  3) Uninstall' '操作：1) 安装  2) 更新  3) 卸载')" 1
    case "$REPLY" in 1 | install)
      action=install
      break
      ;;
    2 | update)
      action=update
      break
      ;;
    3 | uninstall)
      action=uninstall
      break
      ;;
    esac
  done
fi
case "$action" in install | update | uninstall) ;; *) fail 'invalid action' ;; esac
if [ "$dry_run" = 0 ]; then
  for tool in tar sha256sum awk sed grep readlink seq find stat ln; do command -v "$tool" >/dev/null || fail "missing prerequisite: $tool"; done
  if [ "$action" != uninstall ]; then command -v curl >/dev/null || fail 'missing prerequisite: curl'; fi
fi
record() {
  local key="$1" value="$2" temporary
  [ -n "$state" ] || return 0
  temporary="$(mktemp "$state/.answer.XXXXXX")"
  printf '%s\n' "$value" >"$temporary"
  if ! ln "$temporary" "$state/$key" 2>/dev/null; then
    [[ -f "$state/$key" && ! -L "$state/$key" && "$(cat "$state/$key")" = "$value" ]] || fail 'setup progress changed concurrently; rerun to resume'
  fi
  rm -f -- "$temporary"
}
saved() {
  [ -n "$state" ] && [ -f "$state/$1" ] && [ ! -L "$state/$1" ]
}
finish_progress() {
  if [ -n "$state" ]; then rm -rf -- "$state"; fi
}
if [ "$interactive" = 1 ] && [ "$dry_run" = 0 ]; then
  for directory in /var/lib/ovpn-go /var/lib/ovpn-go/installer "/var/lib/ovpn-go/installer/$action"; do
    [ ! -L "$directory" ] || fail 'unsafe setup progress directory'
    mkdir -p "$directory"
    [ "$(stat -c %u "$directory")" = 0 ] || fail 'setup progress must be owned by root'
    chmod 700 "$directory"
  done
  state="/var/lib/ovpn-go/installer/$action"
  if saved format; then [ "$(cat "$state/format")" = 1 ] || fail 'unsupported setup progress format'; else record format 1; fi
fi
if [ -z "$root" ]; then
  root=/opt/ovpn
  if [ -L "$link" ]; then
    existing="$(readlink -f "$link" || true)"
    if [ -n "$existing" ] && [ -f "${existing%/*}/.ovpn-install" ]; then root="${existing%/*}"; fi
  fi
fi
if [ "$interactive" = 1 ]; then
  if saved directory; then
    root="$(cat "$state/directory")"
    say "Resuming $action in $root; completed answers are retained." "继续 $root 的 $action 操作；已完成的答案会保留。"
  else
    read_value "$(say 'Installation directory' '安装目录')" "$root"
    root="$REPLY"
  fi
fi
case "$root" in /*) ;; *) fail 'installation directory must be absolute' ;; esac
case "$root" in / | /opt | /usr | /usr/local | /usr/local/bin | *$'\n'* | *$'\r'* | *'"'* | *'%'* | *\\* | *'$'* | *'`'* | *'/../'* | *'/./'* | */.. | */.) fail 'unsafe installation directory' ;; esac
root="${root%/}"
[ ! -L "$root" ] || fail 'installation directory must not be a symlink'
parent="$(dirname "$root")"
if [ "$dry_run" = 0 ]; then
  mkdir -p "$parent"
  root="$(cd "$parent" && pwd -P)/$(basename "$root")"
fi
record directory "$root"
[ "$link" != "$root/ovpn" ] || fail 'command link must be outside the installation'
if [ -e "$link" ] || [ -L "$link" ]; then
  if [ ! -L "$link" ] || [ "$(readlink "$link")" != "$root/ovpn" ]; then fail "refusing to replace existing command: $link"; fi
fi
manager=''
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  manager=systemd
elif command -v rc-service >/dev/null && [ -d /run/openrc ]; then
  manager=openrc
elif [ "$dry_run" = 1 ]; then
  manager=unavailable
else fail 'no running systemd/OpenRC found; use manual foreground deployment'; fi
journal="$root/.update-backup"
lockfile="$root.install-lock"
if [ "$manager" = systemd ]; then unit=/etc/systemd/system/ovpn.service; else unit=/etc/init.d/ovpn; fi
service() {
  if [ "$manager" = systemd ]; then systemctl "$1" ovpn.service; else rc-service ovpn "$1"; fi
}
healthy() {
  local _
  for _ in $(seq 1 60); do
    if "$root/ovpn" runtime health >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}
acquire_lock() {
  [ "$install_locked" = 0 ] || return 0
  if [ ! -e "$lockfile" ] && [ ! -L "$lockfile" ]; then (
    set -C
    : >"$lockfile"
  ) || fail 'installation lock creation raced another process; retry'; fi
  [[ -f "$lockfile" && ! -L "$lockfile" ]] || fail 'unsafe installation lock'
  exec 9<>"$lockfile"
  "$1" -n 9 || fail 'another installation/update is active'
  install_locked=1
}
# One renderer is used for fresh installs and transactional service migration.
render_service() {
  if [ "$manager" = systemd ]; then
    cat <<SERVICE
[Unit]
Description=OpenVPN server
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart="$root/ovpn" server run
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
KillMode=mixed
[Install]
WantedBy=multi-user.target
SERVICE
  else
    cat <<SERVICE
#!/sbin/openrc-run
name="ovpn"
command="$root/ovpn"
pidfile="/run/ovpn-service.pid"
output_log="$root/run/service.log"
error_log="$root/run/service-error.log"
depend() { need net; }
start() {
 ebegin "Starting ovpn"
 start-stop-daemon --start --background --make-pidfile --pidfile "\$pidfile" --exec "\$command" --stdout "\$output_log" --stderr "\$error_log" -- server run
 eend \$?
}
stop() {
 ebegin "Stopping ovpn"
 if start-stop-daemon --stop --test --quiet --pidfile "\$pidfile"; then
  start-stop-daemon --stop --retry TERM/30/KILL/5 --pidfile "\$pidfile" || { eend 1; return 1; }
 fi
 rm -f "\$pidfile"
 eend 0
}
status() {
 if start-stop-daemon --stop --test --quiet --pidfile "\$pidfile"; then einfo "ovpn is started"; return 0; fi
 einfo "ovpn is stopped"
 return 3
}
SERVICE
  fi
}
check_service_owner() {
  [[ -f "$unit" && ! -L "$unit" ]] || fail 'service definition does not belong to this installation'
  if [ "$manager" = systemd ]; then
    grep -Fxq "ExecStart=\"$root/ovpn\"" "$unit" || grep -Fxq "ExecStart=\"$root/ovpn\" server run" "$unit" || fail 'service definition does not belong to this installation'
  else
    grep -Fxq "command=\"$root/ovpn\"" "$unit" || fail 'service definition does not belong to this installation'
  fi
}
reload_service() {
  if [ "$manager" = systemd ]; then systemctl daemon-reload; fi
}
write_service() {
  if [ -f "$unit" ]; then cp -p "$unit" "$work/ovpn-service"; else
    : >"$work/ovpn-service"
    if [ "$manager" = systemd ]; then chmod 644 "$work/ovpn-service"; else chmod 755 "$work/ovpn-service"; fi
  fi
  render_service >"$work/ovpn-service"
  # Place the temporary file on the service filesystem before atomic replacement.
  local replacement
  replacement="$(mktemp "${unit}.XXXXXX")"
  cp -p "$work/ovpn-service" "$replacement"
  mv -f "$replacement" "$unit"
  reload_service
}
begin_update() {
  check_service_owner
  was_running=0
  if service status >/dev/null 2>&1; then
    was_running=1
  elif "$root/ovpn" runtime status >/dev/null 2>&1; then fail 'unmanaged instance detected; stop it before updating'; fi
  mkdir "$journal"
  printf '%s\n' "$was_running" >"$journal/was-running"
  transaction=1
  if [ "$was_running" = 1 ]; then service stop; fi
  if [ -f "$root/run/.server.lock" ]; then
    exec 8<>"$root/run/.server.lock"
    "$1" -n 8 || fail 'unmanaged instance detected; stop it before updating'
  fi
  for name in ovpn bin runtime MANIFEST.sha256 config data run; do
    if [ -e "$root/$name" ] || [ -L "$root/$name" ]; then cp -a "$root/$name" "$journal/$name"; fi
  done
  cp -a "$unit" "$journal/service-definition"
  : >"$journal/ready"
}
finish_update() {
  exec 8>&-
  if [ "$was_running" = 1 ]; then
    service start
    healthy || fail 'updated service failed health checks'
  fi
  transaction=0
  rm -rf -- "$journal"
}
refresh_service() {
  check_service_owner
  render_service >"$work/expected-service"
  if "$root/bin/cmp" -s "$work/expected-service" "$unit"; then return; fi
  begin_update "$root/bin/flock"
  write_service
  finish_update
  say 'Migrated service to ovpn server run; original service state restored.' '已将服务入口迁移至 ovpn server run，并恢复原服务状态。'
}
rollback() {
  [ -d "$journal" ] || return 0
  if [ ! -f "$journal/ready" ]; then
    if [ "$(cat "$journal/was-running" 2>/dev/null)" = 1 ]; then service start || return 1; fi
    rm -rf -- "$journal"
    return 0
  fi
  service stop >/dev/null 2>&1 || true
  for name in ovpn bin runtime MANIFEST.sha256 config data run; do
    rm -rf -- "${root:?}/$name"
    if [ -e "$journal/$name" ] || [ -L "$journal/$name" ]; then cp -a "$journal/$name" "$root/$name"; fi
  done
  if [ -f "$journal/service-definition" ]; then
    cp -a "$journal/service-definition" "$unit"
    reload_service
  fi
  if [ "$(cat "$journal/was-running")" = 1 ]; then
    if ! service start || ! healthy; then
      printf 'ovpn: rollback restored files, but service recovery failed; backup: %s\n' "$journal" >&2
      return 1
    fi
  fi
  rm -rf -- "$journal"
}
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ "$wizard_starting" = 1 ]; then service stop >/dev/null 2>&1 || true; fi
  if [ "$status" = 130 ] && [ -n "$state" ]; then say 'Interrupted; rerun and select the same action to resume.' '操作中断；重新运行并选择相同操作即可继续。'; fi
  if [ -n "$candidate" ]; then rm -f -- "$candidate"; fi
  exec 8>&-
  if [ "$transaction" = 1 ]; then rollback || status=1; fi
  if [ "$status" != 0 ] && [ "$new_install" = 1 ]; then
    if [ "$new_service" = 1 ]; then
      if [ "$manager" = systemd ]; then
        rm -f /etc/systemd/system/ovpn.service
        systemctl daemon-reload || true
      else rm -f /etc/init.d/ovpn; fi
    fi
    if [ -L "$link" ] && [ "$(readlink "$link")" = "$root/ovpn" ]; then rm -f "$link"; fi
    rm -rf -- "$root"
  fi
  rm -rf -- "$work"
  if [ -n "$staging" ]; then rm -rf -- "$staging"; fi
  exec 9>&-
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
# Answers are saved separately from the active YAML. Never evaluate saved input.
answer() {
  local key="$1" en="$2" zh="$3" default="$4" kind="${5:-text}"
  if saved "$key"; then
    REPLY="$(cat "$state/$key")"
    return
  fi
  while true; do
    local displayed_default="$default"
    if [ "$kind" = bool ]; then
      if [ "$default" = true ]; then displayed_default=yes; else displayed_default=no; fi
    fi
    read_value "$(say "$en" "$zh")" "$displayed_default"
    case "$kind" in
    bool) case "${REPLY,,}" in y | yes | true | 1 | 是) REPLY=true ;; n | no | false | 0 | 否) REPLY=false ;; *)
      say 'Enter yes or no.' '请输入 yes 或 no。'
      continue
      ;;
    esac ;;
    version) [[ "$REPLY" = latest || "$REPLY" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue ;;
    required) [ -n "$REPLY" ] || {
      say 'A value is required.' '此项不能为空。'
      continue
    } ;;
    esac
    [[ "$REPLY" != *$'\r'* && "$REPLY" != *$'\n'* ]] || continue
    return
  done
}
quote_yaml() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}
render_config() {
  local item
  printf 'version: 1\nserver:\n  endpoint: '
  quote_yaml "$endpoint"
  printf '\n  transport:\n    protocol: '
  quote_yaml "$protocol"
  printf '\n    family: '
  quote_yaml "$family"
  printf '\n    port: %s\n  clientToClient: %s\nipv4:\n  network: ' "$port" "$client_to_client"
  quote_yaml "$network"
  if [ "$pool" != auto ]; then printf '\n  dynamicPoolSize: %s' "$pool"; fi
  printf '\n  nat:\n    enabled: %s\n    interface: ' "$nat"
  quote_yaml "$interface"
  printf '\n  redirectGateway: %s\n  dns: [' "$redirect"
  local separator=''
  if [ "$dns" != none ]; then
    IFS=, read -ra items <<<"$dns"
    for item in "${items[@]}"; do
      printf '%s' "$separator"
      quote_yaml "$item"
      separator=', '
    done
  fi
  printf ']\n  routes: ['
  separator=''
  if [ "$routes" != none ]; then
    IFS=, read -ra items <<<"$routes"
    for item in "${items[@]}"; do
      printf '%s' "$separator"
      quote_yaml "$item"
      separator=', '
    done
  fi
  printf ']\napi:\n  enabled: %s\n  listenIP: ' "$api_enabled"
  quote_yaml "$api_listen_ip"
  printf '\n  port: %s\n  cors: [' "$api_port"
  separator=''
  if [ "$api_cors" != none ]; then
    IFS=, read -ra items <<<"$api_cors"
    for item in "${items[@]}"; do
      printf '%s' "$separator"
      quote_yaml "$item"
      separator=', '
    done
  fi
  printf ']\nlogging:\n  maxBytes: %s\n  backups: %s\n' "$log_bytes" "$log_backups"
}
config_answer() {
  local key="$1" en="$2" zh="$3" default="$4" kind="${5:-text}" value
  if saved "$key"; then
    printf -v "$key" '%s' "$(cat "$state/$key")"
    return
  fi
  while true; do
    answer "$key" "$en" "$zh" "$default" "$kind"
    value="$REPLY"
    case "$key" in port | api_port | log_bytes | log_backups) [[ "$value" =~ ^[0-9]+$ ]] || continue ;; pool) [[ "$value" = auto || "$value" =~ ^[0-9]+$ ]] || continue ;; esac
    printf -v "$key" '%s' "$value"
    render_config >"$work/candidate.yaml"
    if [ "$dry_run" = 1 ] || OVPN_CONFIG_FILE="$work/candidate.yaml" "$root/ovpn" config validate >"$work/validation" 2>&1; then
      record "$key" "$value"
      return
    fi
    cat "$work/validation" >&2
    say 'Please correct this value.' '请修改此项。'
    printf -v "$key" '%s' "$default"
  done
}
setup() {
  [ "$interactive" = 1 ] || return 0
  # Keep the installed runtime when configuration is interrupted.
  new_install=0
  new_service=0
  local endpoint=vpn.example.com protocol=udp family=auto port=1194 client_to_client=true
  local network=10.42.0.0/24 pool=auto nat=false interface=auto redirect=false dns=none routes=none log_bytes=10485760 log_backups=5
  local api_enabled=false api_listen_ip=127.0.0.1 api_port=11940 api_cors=none
  local advanced start_now enable_boot
  if [ ! -e "$root/config/config.yaml" ]; then
    config_answer endpoint 'Public hostname or IP (required)' '公网域名或 IP（必填）' '' required
    config_answer network 'VPN IPv4 network' 'VPN IPv4 网段' 10.42.0.0/24
    config_answer protocol 'Transport protocol (udp/tcp)' '传输协议（udp/tcp）' udp
    config_answer port 'VPN port' 'VPN 端口' 1194
    config_answer nat 'Enable NAT (yes/no)' '启用 NAT（yes/no）' false bool
    config_answer redirect 'Route client Internet traffic through VPN (yes/no)' '客户端互联网流量经过 VPN（yes/no）' false bool
    config_answer dns 'DNS servers (comma-separated IPv4, or none)' 'DNS 服务器（IPv4，逗号分隔，或 none）' none
    config_answer api_enabled 'Enable REST API (yes/no)' '启用 REST API（yes/no）' false bool
    if [ "$api_enabled" = true ]; then
      config_answer api_listen_ip 'API listen IP' 'API 监听 IP' 127.0.0.1
      config_answer api_port 'API port' 'API 端口' 11940
      config_answer api_cors 'CORS hosts (comma-separated host[:port]; * allows all; none disables)' 'CORS 来源（host[:port]，逗号分隔；* 允许全部；none 关闭）' none
    fi
    answer advanced 'Advanced settings (yes/no)' '高级设置（yes/no）' false bool
    advanced="$REPLY"
    record advanced "$advanced"
    if [ "$advanced" = true ]; then
      config_answer family 'Transport family (auto/ipv4/ipv6)' '传输地址族（auto/ipv4/ipv6）' auto
      config_answer pool 'Dynamic address pool size (auto or number)' '动态地址池大小（auto 或数字）' auto
      config_answer client_to_client 'Allow client-to-client traffic (yes/no)' '允许客户端互通（yes/no）' true bool
      config_answer interface 'NAT interface (auto or interface name)' 'NAT 网卡（auto 或网卡名称）' auto
      config_answer routes 'Pushed routes (comma-separated IPv4 CIDRs, or none)' '推送路由（IPv4 CIDR，逗号分隔，或 none）' none
      config_answer log_bytes 'Log rotation size in bytes' '日志轮转大小（字节）' 10485760
      config_answer log_backups 'Log backup count' '日志备份数量' 5
    fi
    render_config >"$work/candidate.yaml"
    if [ "$dry_run" = 0 ]; then OVPN_CONFIG_FILE="$work/candidate.yaml" "$root/ovpn" config validate; fi
    cat "$work/candidate.yaml"
    answer confirmed 'Write this configuration (yes/no)' '写入以上配置（yes/no）' true bool
    [ "$REPLY" = true ] || {
      if [ "$dry_run" = 1 ]; then
        say 'Preview cancelled; answers were discarded.' '已取消试运行；答案不会保留。'
      else say 'Configuration saved as a draft; rerun to continue.' '配置草稿已保存；重新运行即可继续。'; fi
      exit 0
    }
    record confirmed true
    if [ "$dry_run" = 1 ]; then
      say "DRY RUN: would write $root/config/config.yaml after package validation." "试运行：通过程序验证后将写入 $root/config/config.yaml。"
    elif [ -e "$root/config/config.yaml" ]; then
      "$root/bin/cmp" -s "$work/candidate.yaml" "$root/config/config.yaml" || fail 'configuration changed outside the installer; refusing to overwrite it'
    else
      candidate="$(mktemp "$root/config/.config.XXXXXX")"
      cat "$work/candidate.yaml" >"$candidate"
      ln "$candidate" "$root/config/config.yaml" || fail 'configuration already exists'
      rm -f "$candidate"
      candidate=''
    fi
  else
    if [ "$dry_run" = 0 ]; then OVPN_CONFIG_FILE="$root/config/config.yaml" "$root/ovpn" config validate; fi
    say 'Existing configuration is retained.' '保留已有配置。'
  fi
  answer enable_boot 'Enable service at boot (yes/no)' '开机自动启动（yes/no）' true bool
  enable_boot="$REPLY"
  record enable_boot "$enable_boot"
  answer start_now 'Start service now (yes/no)' '现在启动服务（yes/no）' true bool
  start_now="$REPLY"
  record start_now "$start_now"
  if [ "$dry_run" = 1 ]; then
    say "DRY RUN: enable at boot=$enable_boot; start now=$start_now; no service changes." "试运行：开机启动=$enable_boot；立即启动=$start_now；不会修改服务。"
    return
  fi
  if [ "$enable_boot" = true ]; then
    if [ "$manager" = systemd ]; then systemctl enable ovpn.service; else rc-update add ovpn default; fi
  fi
  if [ "$start_now" = true ]; then
    if ! service status >/dev/null 2>&1; then
      wizard_starting=1
      service start
    fi
    healthy || fail 'service failed health checks; configuration is retained; rerun to retry'
    wizard_starting=0
  fi
  say 'First start initializes empty data from config.yaml; no manual init/apply is needed. After initialization, edit with ovpn config edit --apply; restarting alone keeps the applied configuration.' '首次启动会使用 config.yaml 初始化空数据，无需手动 init/apply。初始化后可用 ovpn config edit --apply 修改并应用配置；单纯重启保留已应用配置。'
  say "Installation ready: $root. Use: sudo ovpn runtime health" "安装完成：$root。检查状态：sudo ovpn runtime health"
}
uninstall() {
  [[ -f "$root/.ovpn-install" && ! -L "$root/.ovpn-install" ]] || fail 'not an installer-managed installation'
  [ "$(cat "$root/.ovpn-install")" = "$manager" ] || fail 'service manager changed'
  acquire_lock "$root/bin/flock"
  check_service_owner
  local keep=true
  if [ "$interactive" = 1 ]; then
    answer keep_data 'Keep configuration, certificates, database and logs (yes/no)' '保留配置、证书、数据库和日志（yes/no）' true bool
    keep="$REPLY"
    record keep_data "$keep"
    if [ "$keep" = false ]; then
      answer delete_data 'Type DELETE to permanently delete instance data' '输入 DELETE 永久删除实例数据' '' required
      [ "$REPLY" = DELETE ] || fail 'data deletion not confirmed'
      record delete_data DELETE
    fi
    answer uninstall_confirmed 'Uninstall ovpn (yes/no)' '确认卸载 ovpn（yes/no）' false bool
    [ "$REPLY" = true ] || {
      say 'Uninstall cancelled.' '已取消卸载。'
      finish_progress
      return
    }
    record uninstall_confirmed true
  else fail 'uninstall requires interactive confirmation'; fi
  if ! service status >/dev/null 2>&1 && "$root/ovpn" runtime status >/dev/null 2>&1; then fail 'unmanaged instance detected; stop it before uninstalling'; fi
  check_service_owner
  if [ -d "$journal" ]; then rollback || fail 'interrupted update recovery failed'; fi
  service stop
  if [ -f "$root/run/.server.lock" ]; then
    exec 8<>"$root/run/.server.lock"
    "$root/bin/flock" -n 8 || fail 'unmanaged instance detected'
  fi
  if [ "$manager" = systemd ]; then systemctl disable ovpn.service; else rc-update del ovpn default || true; fi
  # Complete the short removal section once confirmed; prompts and service stop remain interruptible.
  trap '' INT TERM HUP
  rm -f "$unit"
  if [ "$manager" = systemd ]; then systemctl daemon-reload; fi
  if [ -L "$link" ] && [ "$(readlink "$link")" = "$root/ovpn" ]; then rm -f "$link"; fi
  # Preserve an ownership marker for a later installation with retained data.
  printf '%s\n' "$manager" >"$root/.ovpn-retained"
  for name in bin runtime ovpn MANIFEST.sha256; do rm -rf -- "${root:?}/$name"; done
  if [ "$keep" = false ]; then
    for name in config data logs run; do rm -rf -- "${root:?}/$name"; done
    rm -f "$root/.ovpn-retained"
  fi
  rm -f "$root/.ovpn-install"
  rmdir "$root" 2>/dev/null || true
  local pending
  for pending in /var/lib/ovpn-go/installer/install /var/lib/ovpn-go/installer/update; do
    if [ ! -L "$pending" ] && [ -f "$pending/directory" ] && [ "$(cat "$pending/directory")" = "$root" ]; then rm -rf -- "$pending"; fi
  done
  say "Uninstalled ovpn. Retain data: $keep" "已卸载 ovpn。保留数据：$keep"
  finish_progress
}
# Preview is isolated from installer progress, downloads, locks and host mutations.
if [ "$dry_run" = 1 ]; then
  say "DRY RUN: action=$action; directory=$root; command=$link; service manager=$manager" "试运行：操作=$action；目录=$root；命令=$link；服务管理器=$manager"
  if [ "$action" != uninstall ]; then
    if [ "$interactive" = 1 ]; then
      answer version 'Release version (latest or X.Y.Z)' '发布版本（latest 或 X.Y.Z）' "$version" version
      version="$REPLY"
    fi
    case "$(uname -m)" in x86_64) architecture=amd64 ;; aarch64 | arm64) architecture=arm64 ;; *) fail 'unsupported CPU architecture' ;; esac
    say "DRY RUN: would resolve $version and verify the linux-$architecture archive and SHA256SUMS from ${base:-GitHub Releases}; no downloads." "试运行：将解析 $version 并校验 linux-$architecture 压缩包及 SHA256SUMS，来源 ${base:-GitHub Releases}；不会下载。"
    if [ "$action" = install ]; then
      say "DRY RUN: would register $root/ovpn server run with $manager." "试运行：将向 $manager 注册 $root/ovpn server run。"
      setup
    else
      if [ "$interactive" = 1 ]; then
        answer update_confirmed 'Continue update (yes/no)' '继续更新（yes/no）' true bool
        [ "$REPLY" = true ] || exit 0
      fi
      say 'DRY RUN: would lock, snapshot program/data/service definition, update to ovpn server run and health-check; rollback on failure; preserve data and service state.' '试运行：将锁定、快照程序/数据/服务定义，更新至 ovpn server run 并检查健康；失败回滚，保留数据和服务状态。'
    fi
  else
    if [ "$interactive" = 1 ]; then
      answer keep_data 'Keep configuration, certificates, database and logs (yes/no)' '保留配置、证书、数据库和日志（yes/no）' true bool
      keep="$REPLY"
      if [ "$keep" = false ]; then
        answer delete_data 'Type DELETE to permanently delete instance data' '输入 DELETE 永久删除实例数据' '' required
        [ "$REPLY" = DELETE ] || fail 'data deletion not confirmed'
      fi
      answer uninstall_confirmed 'Uninstall ovpn (yes/no)' '确认卸载 ovpn（yes/no）' false bool
      [ "$REPLY" = true ] || exit 0
      say "DRY RUN: would uninstall; keep data=$keep; no files or services changed." "试运行：将卸载；保留数据=$keep；不会修改文件或服务。"
    else say 'DRY RUN: uninstall requires interactive confirmation during a real run; data is retained by default.' '试运行：实际卸载需要交互确认；默认保留数据。'; fi
  fi
  say 'DRY RUN complete. Package verification and runtime health have not been tested.' '试运行完成。尚未执行压缩包验证和运行健康检查。'
  exit 0
fi
if [ "$action" = uninstall ]; then
  uninstall
  exit 0
fi
# Resume setup without downloading the same package again.
if [ "$action" = install ] && [ -f "$root/.ovpn-install" ] && [ -x "$root/ovpn" ]; then
  [ "$(cat "$root/.ovpn-install")" = "$manager" ] || fail 'service manager changed'
  acquire_lock "$root/bin/flock"
  check_service_owner
  if [ -d "$journal" ]; then rollback || fail 'interrupted update recovery failed'; fi
  refresh_service
  setup
  finish_progress
  exit 0
fi
if [ "$interactive" = 1 ]; then
  answer version 'Release version (latest or X.Y.Z)' '发布版本（latest 或 X.Y.Z）' latest version
  version="$REPLY"
  record version "$version"
fi
case "$(uname -m)" in x86_64) architecture=amd64 ;; aarch64 | arm64) architecture=arm64 ;; *) fail 'unsupported CPU architecture' ;; esac
repository=yjrszcq/ovpn-go
if saved resolved_version; then version="$(cat "$state/resolved_version")"; fi
if [ "$version" = latest ]; then
  location="$(curl --fail --silent --show-error --location --retry 3 --output /dev/null --write-out '%{url_effective}' "https://github.com/$repository/releases/latest")"
  version="${location##*/}"
fi
version="${version#v}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'invalid release version'
record resolved_version "$version"
archive="openvpn-server-$version-linux-$architecture.tar.gz"
base="${base:-https://github.com/$repository/releases/download/v$version}"
if [ "$interactive" = 1 ] && [ "$action" = update ]; then
  say "Target release: $version" "目标版本：$version"
  answer update_confirmed 'Continue update (yes/no)' '继续更新（yes/no）' true bool
  [ "$REPLY" = true ] || {
    finish_progress
    exit 0
  }
  record update_confirmed true
fi
curl --fail --silent --show-error --location --retry 3 "$base/$archive" -o "$work/$archive"
curl --fail --silent --show-error --location --retry 3 "$base/SHA256SUMS" -o "$work/SHA256SUMS"
checksum="$(awk -v name="$archive" '$2 == name {print $1}' "$work/SHA256SUMS")"
[[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || fail 'missing or duplicate archive checksum'
(cd "$work" && printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c -)
# Validate every member before extraction, including links. No devices, hard
# links, absolute paths, parent traversal or symlinked extraction directories.
tar -tzf "$work/$archive" >"$work/members"
awk '
 /^openvpn-server\/bin\/\[\[?$/ {next}
 !/^openvpn-server\/[A-Za-z0-9_.+@,=\/-]*$/ {exit 1}
 /(^|\/)\.\.?($|\/)/ {exit 1}
 END {if (NR == 0) exit 1}
' "$work/members" || fail 'unsafe archive member'
tar -tvzf "$work/$archive" >"$work/details"
awk '
 substr($1,1,1) !~ /^[-dl]$/ {exit 1}
 substr($1,1,1) == "l" {
   if ($(NF-1) != "->") exit 1
   name=$(NF-2); target=$NF
   if (name ~ /^openvpn-server\/bin\// && target == "../ovpn") next
   if (name == "openvpn-server/logs" && target == "data/logs") next
   if (target !~ /^[A-Za-z0-9_.+-]+$/ || target == "." || target == "..") exit 1
 }
' "$work/details" || fail 'unsafe archive type or link'
# Reject any member beneath a symlink, regardless of archive ordering.
awk 'substr($1,1,1)=="l" {print $(NF-2)}' "$work/details" >"$work/links"
while IFS= read -r target; do
  if awk -v prefix="$target/" 'index($0,prefix)==1 {found=1} END {exit !found}' "$work/members"; then fail 'archive writes through a symlink'; fi
done <"$work/links"
mkdir "$work/extract"
tar -xzf "$work/$archive" -C "$work/extract"
package="$work/extract/openvpn-server"
if [ ! -f "$package/MANIFEST.sha256" ] || [ -L "$package/MANIFEST.sha256" ]; then fail 'package manifest missing'; fi
awk '$1 !~ /^[0-9a-f]+$/ || length($1)!=64 || NF!=2 || $2 !~ /^(ovpn|runtime\/[A-Za-z0-9_.+@,=\/-]+|config\/config.example.yaml)$/ || $2 ~ /(^|\/)\.\.?($|\/)/ {exit 1}' "$package/MANIFEST.sha256" || fail 'unsafe file manifest'
(cd "$package" && sha256sum -c MANIFEST.sha256 >/dev/null) || fail 'file manifest verification failed'
# Every regular file must be listed, and every listed path must be regular.
awk '{print $2}' "$package/MANIFEST.sha256" >"$work/expected-files"
(cd "$package" && find . -type f ! -path ./MANIFEST.sha256 | sed 's|^./||') >"$work/actual-files"
awk 'FILENAME==ARGV[1] {expected[$0]++; count++; next} {if (!expected[$0]--) bad=1; actual++} END {exit (bad || actual!=count)}' "$work/expected-files" "$work/actual-files" || fail 'package file inventory mismatch'
manifest="$package/runtime/manifest.json"
grep -Fq "\"version\": \"$version\"" "$manifest" || fail 'package version mismatch'
grep -Fq "\"architecture\": \"$architecture\"" "$manifest" || fail 'package architecture mismatch'
grep -Eq '"data_schema": 4([,[:space:]]|$)' "$manifest" || fail 'unsupported data schema'
[ "$("$package/ovpn" version --short)" = "$version" ] || fail 'packaged executable version mismatch'
acquire_lock "$package/bin/flock"
retained=0
if [ -e "$root" ] && [ ! -f "$root/.ovpn-install" ] && [ -f "$root/.ovpn-retained" ] && [ ! -L "$root/.ovpn-retained" ]; then
  [[ "$action" = install && "$(cat "$root/.ovpn-retained")" = "$manager" ]] || fail 'invalid retained installation'
  retained=1
fi
if [ -e "$root" ] && [ "$retained" = 0 ]; then
  [ -f "$root/.ovpn-install" ] || fail 'existing directory is not an ovpn installation'
  [ "$(cat "$root/.ovpn-install")" = "$manager" ] || fail 'service manager changed'
  check_service_owner
  if [ -d "$journal" ]; then rollback || fail 'interrupted update recovery failed'; fi
  grep -Eq '"data_schema": 4([,[:space:]]|$)' "$root/runtime/manifest.json" || fail 'installed data schema is incompatible'
  [ -x "$root/ovpn" ] || fail 'existing executable is missing'
  old_version="$("$root/ovpn" version --short)"
  [[ "$old_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'invalid installed version'
  IFS=. read -r old_major old_minor old_patch <<<"$old_version"
  IFS=. read -r new_major new_minor new_patch <<<"$version"
  if ((10#$new_major < 10#$old_major || (10#$new_major == 10#$old_major && 10#$new_minor < 10#$old_minor) || (10#$new_major == 10#$old_major && 10#$new_minor == 10#$old_minor && 10#$new_patch < 10#$old_patch))); then fail 'downgrade refused'; fi
  [ "$action" = update ] || {
    refresh_service
    setup
    finish_progress
    exit 0
  }
  if [ "$old_version" = "$version" ] && "$package/bin/cmp" -s "$root/MANIFEST.sha256" "$package/MANIFEST.sha256"; then
    refresh_service
    say "ovpn is already $version" "ovpn 已是 $version"
    finish_progress
    exit 0
  fi
  begin_update "$package/bin/flock"
  for name in ovpn bin runtime MANIFEST.sha256; do
    rm -rf -- "${root:?}/$name"
    cp -a "$package/$name" "$root/$name"
  done
  cp -a "$package/config/config.example.yaml" "$root/config/config.example.yaml"
  write_service
  finish_update
  say "Updated ovpn $old_version -> $version; original service state restored" "已更新 ovpn $old_version -> $version；已恢复原服务状态"
else
  [ "$action" = install ] || fail 'no installation to update'
  if [ "$manager" = systemd ]; then
    if [ -e /etc/systemd/system/ovpn.service ] || [ -L /etc/systemd/system/ovpn.service ]; then fail 'existing ovpn.service must not be replaced'; fi
  else
    if [ -e /etc/init.d/ovpn ] || [ -L /etc/init.d/ovpn ]; then fail 'existing OpenRC ovpn service must not be replaced'; fi
  fi
  staging="$(mktemp -d "$parent/.ovpn-install.XXXXXX")"
  cp -a "$package/." "$staging/"
  printf '%s\n' "$manager" >"$staging/.ovpn-install"
  if [ "$retained" = 1 ]; then
    for name in ovpn bin runtime MANIFEST.sha256; do cp -a "$staging/$name" "$root/$name"; done
    mkdir -p "$root/config" "$root/data" "$root/run"
    cp -a "$staging/config/config.example.yaml" "$root/config/config.example.yaml"
    cp "$staging/.ovpn-install" "$root/.ovpn-install"
    rm -f "$root/.ovpn-retained"
  else
    mv "$staging" "$root"
    new_install=1
  fi
  mkdir -p "$(dirname "$link")"
  ln -s "$root/ovpn" "$link"
  if [ -e "$unit" ] || [ -L "$unit" ]; then fail 'existing service must not be replaced'; fi
  new_service=1
  write_service
  say "Installed ovpn $version at $root" "已安装 ovpn $version：$root"
  setup
fi

finish_progress
