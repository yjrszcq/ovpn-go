#!/usr/bin/env python3
"""Preview all installer actions with real terminal input and no installation."""
import os
import fcntl
from pathlib import Path
import pty
import select
import signal
import subprocess
import tempfile
import termios
import time

SCRIPT = Path(__file__).resolve().parents[3] / 'install.sh'
VERSION = next(line.split('=', 1)[1] for line in (SCRIPT.parent / 'versions.env').read_text().splitlines()
               if line.startswith('IMAGE_VERSION='))
with tempfile.TemporaryDirectory(prefix='ovpn-preview-') as directory:
    root = Path(directory)
    tools = root / 'tools'
    tools.mkdir()
    # Any download or service call must fail the test, even if its exit is ignored.
    for name in ['curl', 'systemctl', 'rc-service', 'rc-update']:
        tool = tools / name
        tool.write_text('#!/bin/sh\nprintf forbidden >>"$PREVIEW_SENTINEL"\nexit 97\n')
        tool.chmod(0o755)
    env = dict(os.environ, PATH=str(tools) + ':' + os.environ['PATH'],
               PREVIEW_SENTINEL=str(root / 'forbidden'))
    target = root / 'missing parent' / 'installation'
    for language, action, special in [('1', '1', False), ('2', '1', True),
                                      ('1', '2', False), ('2', '3', False), ('1', '3', True)]:
        master, slave = pty.openpty()
        process = subprocess.Popen(['bash', str(SCRIPT), '--dry-run', '--directory', str(target),
                                    '--command-link', str(root / 'ovpn')],
                                   stdin=slave, stdout=slave, stderr=slave, env=env)
        os.close(slave)
        # TTY canonical input buffers answers, including defaults.
        answers = [language, action, '']
        if action != '3':
            answers.append(VERSION)
        if action == '1':
            answers += ['vpn.example.com'] + [''] * 6 + ['yes' if special else '']
            if special:
                answers += [''] * 3  # API listen IP, port and disabled CORS defaults
            answers += [''] * 4  # advanced, write draft, enable at boot, start
        if action == '2':
            answers.append('')
        if action == '3':
            answers += ['no', 'DELETE', 'yes'] if special else ['', 'yes']
        os.write(master, ('\n'.join(answers) + '\n').encode())
        output = b''
        deadline = time.monotonic() + 20
        while True:
            if time.monotonic() > deadline:
                process.kill()
                raise AssertionError(output.decode(errors='replace'))
            if select.select([master], [], [], .1)[0]:
                try:
                    chunk = os.read(master, 65536)
                    if not chunk:
                        break
                    output += chunk
                except OSError:
                    break
            elif process.poll() is not None:
                break
        process.wait(timeout=3)
        os.close(master)
        assert process.returncode == 0, output.decode(errors='replace')
        if action == '1':
            assert b'cors: []' in output, output
            if special:
                assert b'enabled: true' in output, output
        assert not target.parent.exists(), output
        assert not (root / 'ovpn').exists()
        assert not (root / 'forbidden').exists(), output
    result = subprocess.run(['bash', str(SCRIPT), '--dry-run', '--non-interactive',
                             '--directory', str(target)], env=env, capture_output=True)
    assert result.returncode == 0, result.stderr
    assert not target.parent.exists()
    # Interrupt immediately at both version selection and the service question
    # reached after configuration has been confirmed.
    for language, stop_at_boot in [('1', False), ('2', False), ('1', True), ('2', True)] * 25:
        master, slave = pty.openpty()
        def terminal():
            for number in (signal.SIGINT, signal.SIGQUIT, signal.SIGHUP, signal.SIGTERM):
                signal.signal(number, signal.SIG_DFL)
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        process = subprocess.Popen(['bash', str(SCRIPT), '--dry-run', '--directory', str(target),
                                    '--command-link', str(root / 'ovpn')],
                                   stdin=slave, stdout=slave, stderr=slave, env=env,
                                   preexec_fn=terminal, close_fds=True)
        os.close(slave)
        output = b''
        transcript = b''
        try:
            prompts = [('Language / 语言', language),
                       ('操作：' if language == '2' else 'Action:', '1' if stop_at_boot else '2'),
                       ('安装目录' if language == '2' else 'Installation directory', ''),
                       ('发布版本' if language == '2' else 'Release version', '' if stop_at_boot else None)]
            if stop_at_boot:
                labels = (['公网域名或 IP', 'VPN IPv4 网段', '传输协议', 'VPN 端口', '启用 NAT',
                           '客户端互联网流量', 'DNS 服务器', '启用 REST API', '高级设置', '写入以上配置']
                          if language == '2' else
                          ['Public hostname', 'VPN IPv4 network', 'Transport protocol', 'VPN port', 'Enable NAT',
                           'Route client Internet', 'DNS servers', 'Enable REST API', 'Advanced settings',
                           'Write this configuration'])
                prompts += [(label, 'vpn.example.com' if index == 0 else '') for index, label in enumerate(labels)]
                prompts.append(('开机自动启动' if language == '2' else 'Enable service at boot', None))
            for prompt, answer in prompts:
                deadline = time.monotonic() + 10
                while prompt.encode() not in output:
                    assert time.monotonic() < deadline, output.decode(errors='replace')
                    if select.select([master], [], [], .1)[0]:
                        try:
                            chunk = os.read(master, 65536)
                        except OSError:
                            raise AssertionError(f'Installer exited before {prompt!r}: {transcript!r}') from None
                        assert chunk, transcript
                        output += chunk
                        transcript += chunk
                output = output.split(prompt.encode(), 1)[1]
                os.write(master, b'\x03' if answer is None else answer.encode() + b'\n')
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                raise AssertionError(f'Immediate Ctrl-C did not exit (language={language}, service={stop_at_boot}): {transcript!r}') from None
            assert process.returncode == 130, process.returncode
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            os.close(master)
        assert not target.parent.exists()
        assert not (root / 'forbidden').exists()
print('Installer dry-run: bilingual install/update/uninstall without mutations: PASS')
print('Installer immediate Ctrl-C at version/service prompts: 100 bilingual sessions PASS')
