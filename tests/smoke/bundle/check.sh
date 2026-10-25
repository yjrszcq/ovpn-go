#!/usr/bin/env bash
# Native CI acceptance gate. A selected distribution is a complete independent job.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
architecture="${1:?amd64 or arm64 required}"
distribution="${2:-all}"
case "$architecture" in amd64 | arm64) ;; *) exit 64 ;; esac
# shellcheck source=../../../versions.env
. "$ROOT_DIR/versions.env"
archive="$ROOT_DIR/temp/dist/openvpn-server-$IMAGE_VERSION-linux-$architecture.tar.gz"
matrix="$ROOT_DIR/packaging/bundle/test-matrix.json"
# Validate the complete inventory before any lengthy build or network acceptance.
python3 "$ROOT_DIR/tests/smoke/bundle/validation-matrix.py" "$matrix" >/dev/null
if [ "$distribution" = all ]; then
  selected="$(jq -er '.distributions[].id' "$matrix")"
  while IFS= read -r distribution; do
    "$0" "$architecture" "$distribution"
  done <<<"$selected"
  "$0" "$architecture" arch
  "$0" "$architecture" scratch
  exit 0
fi
case "$distribution" in
arch)
  if [ "$architecture" = arm64 ]; then
    "$ROOT_DIR/tests/smoke/bundle/prepare-arch-arm.sh"
  fi
  image="$(jq -er --arg key "arch_$architecture" '.[$key].image' "$matrix")"
  ;;
scratch) image=scratch ;;
*) image="$(jq -er --arg id "$distribution" '.distributions[] | select(.id == $id) | .image' "$matrix")" ;;
esac
logs="$ROOT_DIR/temp/bundle-logs/$architecture/$distribution"
mkdir -p "$logs"
printf 'Verifying %s linux/%s: %s\n' "$distribution" "$architecture" "$image"
"$ROOT_DIR/tests/smoke/bundle/container-smoke.sh" "$archive" "$image" "linux/$architecture" 2>&1 | tee "$logs/container.log"
if [ "$distribution" = scratch ]; then
  DOCKER_DEFAULT_PLATFORM="linux/$architecture" "$ROOT_DIR/tests/smoke/bundle/negative-runtime.sh" "ovpn-bundle-test:scratch-linux-$architecture" 2>&1 | tee "$logs/negative.log"
else
  "$ROOT_DIR/tests/smoke/bundle/vm-smoke.sh" "$archive" "$image" /opt/ovpn "$logs/vm" 2>&1 | tee "$logs/service.log"
fi
