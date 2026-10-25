#!/usr/bin/env python3
"""Exercise delivered rollback functions with injected filesystem failures."""
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[3]
installer = (root / 'install.sh').read_text()
restore = installer[installer.index('restore_mutable() ('):installer.index('begin_update() {')]
rollback = installer[installer.index('rollback() {'):installer.index('cleanup() {')]
for failure in ['none', 'program', 'data', 'unit', 'permissions', 'removal', 'reload']:
    with tempfile.TemporaryDirectory(prefix='ovpn-rollback-io-') as directory:
        target = Path(directory) / 'install'
        journal = target / '.update-backup'
        for name in ['bin', 'runtime', 'config', 'data', 'run']:
            (target / name).mkdir(parents=True)
            (journal / name).mkdir(parents=True)
        (target / 'ovpn').write_text('candidate')
        (journal / 'ovpn').write_text('original')
        (journal / 'data/state').write_text('original data')
        (journal / 'service-definition').write_text('original service')
        (journal / 'was-running').write_text('1\n')
        (journal / 'ready').touch()
        script = r'''
set -euo pipefail
root="$CASE_ROOT/install"
journal="$root/.update-backup"
unit="$CASE_ROOT/unit"
service() { if [ "$1" = start ]; then touch "$CASE_ROOT/started"; fi; }
healthy() { return 0; }
lock_instance() { return 0; }
release_instance_locks() { return 0; }
reload_service() { [ "$FAILURE" != reload ]; }
rm() {
 if [ "$FAILURE" = removal ] && [ "$3" = "$root/ovpn" ]; then return 79; fi
 command rm "$@"
}
cp() {
 case "$FAILURE/$2" in
  "program/$journal/ovpn" | "data/$journal/data/state" | "unit/$journal/service-definition") return 79 ;;
 esac
 command cp "$@"
}
chmod() {
 if [ "$FAILURE" = permissions ] && [ "$2" = "$root/data" ]; then return 79; fi
 command chmod "$@"
}
'''+restore+rollback+r'''
if rollback; then result=0; else result=$?; fi
if [ "$FAILURE" = none ]; then
 test "$result" = 0
 test ! -d "$journal"
 test -e "$CASE_ROOT/started"
else
 test "$result" != 0
 test -f "$journal/ready"
 test ! -e "$CASE_ROOT/started"
fi
'''
        result = subprocess.run(['bash', '-euc', script],
                                env=dict(os.environ, CASE_ROOT=directory, FAILURE=failure))
        assert result.returncode == 0, f'rollback scenario failed: {failure}'
print('Installer rollback: filesystem failures retain snapshot and prevent restart: PASS')
