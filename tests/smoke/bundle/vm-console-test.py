"""Keep VM success/failure markers when init shuts down a delayed console relay."""
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
GUEST = ROOT / 'tests/smoke/bundle/vm-guest.sh'


class Console(unittest.TestCase):
    def test_shutdown_waits_for_pending_console_output(self):
        source = GUEST.read_text()
        for outcome, marker, code in [('reboot', 'OVPN_VM_REBOOT', 0),
                                      ('success', 'OVPN_VM_TEST_PASS', 0),
                                      ('failure', 'OVPN_VM_TEST_FAIL=1', 1)]:
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'install-dir').write_text(str(root))
                relay = root / 'relay.py'
                # Simulate a busy console: the producer can exit before output
                # is forwarded. Init kills the relay as soon as the producer exits.
                relay.write_text('import sys,time\ndata=sys.stdin.buffer.read()\ntime.sleep(.2)\n'
                                 'sys.stdout.buffer.write(data)\nsys.stdout.buffer.flush()\n')
                prefix = source.split('export PATH=', 1)[0]
                prefix = prefix.replace('tee /dev/console', shlex.quote(sys.executable) + ' ' + shlex.quote(str(relay)))
                if outcome == 'reboot':
                    body = '# A second boot proves' + source.split('# A second boot proves', 1)[1]
                    body = body.replace('/fixtures', str(root))
                else:
                    body = f'exit {code}\n'
                # Never call host power, service or log commands. Execute the
                # actual guest bootstrap, exit trap and final reboot branch.
                mocks = ('sync() { :; }; systemctl() { :; }; poweroff() { :; }; '
                         'reboot() { :; }; journalctl() { :; }; tail() { :; }; '
                         'service_action() { :; };\n')
                script = mocks + prefix.replace('/fixtures', str(root)) + '\n' + body
                log = root / 'console.log'
                with log.open('wb') as output:
                    process = subprocess.Popen(['bash', '-c', script], stdout=output,
                                               stderr=subprocess.STDOUT, start_new_session=True)
                    try:
                        result = process.wait(timeout=5)
                    finally:
                        try:
                            os.killpg(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        process.wait()
                self.assertEqual(result, code, log.read_text())
                self.assertIn(marker, log.read_text())


if __name__ == '__main__':
    unittest.main()
