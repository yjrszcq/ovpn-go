"""Interrupt the installer at the blocking-input boundary and preserve slow input."""
import fcntl
import os
from pathlib import Path
import pty
import signal
import subprocess
import tempfile
import termios
import time
import unittest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / 'install.sh'
# Reproduce the pending-signal window immediately before libc blocks for input.
# The timeout path may wait in select/pselect rather than directly in read.
SHIM = r'''
#define _GNU_SOURCE
#include <dlfcn.h>
#include <signal.h>
#include <sys/select.h>
#include <unistd.h>
static int sent;
static void interrupt_input(int input) {
    if (input && !sent && isatty(0)) {
        sent = 1;
        write(2, "INTERRUPT_INJECTED\n", 19);
        raise(SIGINT);
    }
}
ssize_t read(int fd, void *buffer, size_t count) {
    ssize_t (*next)(int,void*,size_t) = dlsym(RTLD_NEXT,"read");
    interrupt_input(fd == 0);
    return next(fd,buffer,count);
}
int select(int n, fd_set *r, fd_set *w, fd_set *e, struct timeval *timeout) {
    int (*next)(int,fd_set*,fd_set*,fd_set*,struct timeval*) = dlsym(RTLD_NEXT,"select");
    interrupt_input(r && FD_ISSET(0,r));
    return next(n,r,w,e,timeout);
}
int pselect(int n, fd_set *r, fd_set *w, fd_set *e,
            const struct timespec *timeout, const sigset_t *mask) {
    int (*next)(int,fd_set*,fd_set*,fd_set*,const struct timespec*,const sigset_t*) = dlsym(RTLD_NEXT,"pselect");
    interrupt_input(r && FD_ISSET(0,r));
    return next(n,r,w,e,timeout,mask);
}
'''


class Input(unittest.TestCase):
    def test_pending_interrupt_exits_without_another_keystroke(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'interrupt.c'
            library = root / 'interrupt.so'
            source.write_text(SHIM)
            subprocess.run(['cc', '-shared', '-fPIC', '-o', str(library), str(source), '-ldl'], check=True)
            master, slave = pty.openpty()
            def terminal():
                for number in (signal.SIGINT, signal.SIGQUIT, signal.SIGHUP, signal.SIGTERM):
                    signal.signal(number, signal.SIG_DFL)
                os.setsid()
                fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
            process = subprocess.Popen(['bash', str(SCRIPT), '--dry-run'],
                                       stdin=slave, stdout=slave, stderr=slave,
                                       env=dict(os.environ, LD_PRELOAD=str(library)),
                                       preexec_fn=terminal)
            os.close(slave)
            try:
                try:
                    code = process.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    self.fail('Pending Ctrl-C required another keystroke to exit')
                self.assertEqual(code, 130)
                output = os.read(master, 65536)
                self.assertIn(b'INTERRUPT_INJECTED', output)
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                os.close(master)

    def test_timeout_preserves_partial_input_and_defaults(self):
        source = SCRIPT.read_text()
        function = 'read_value() {' + source.split('read_value() {', 1)[1].split('\nif [ "$interactive"', 1)[0]
        for chunks, expected in [([b'  path\\with ', b'spaces  \n'], b'  path\\with spaces  '),
                                 ([b'', b'\n'], b'default'),
                                 ([b'\xe9\x85', b'\x8d\xe7\xbd\xae\n'], '配置'.encode())]:
            with self.subTest(expected=expected):
                process = subprocess.Popen(['bash', '-c', 'set -eu; ' + function +
                                            '\nread_value Prompt default; printf "%s" "$REPLY"'],
                                           stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    process.stdin.write(chunks[0])
                    process.stdin.flush()
                    time.sleep(1.2)
                    process.stdin.write(chunks[1])
                    process.stdin.flush()
                    output, error = process.communicate(timeout=4)
                    self.assertEqual(process.returncode, 0, error)
                    self.assertEqual(output, expected)
                finally:
                    if process.poll() is None:
                        process.kill()
                    process.communicate()


if __name__ == '__main__':
    unittest.main()
