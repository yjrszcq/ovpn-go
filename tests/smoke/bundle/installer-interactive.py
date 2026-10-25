#!/usr/bin/env python3
"""Exercise the real installer with a controlling terminal inside the init VM."""
import errno
import fcntl
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import termios
import time

ROOT = Path('/opt/interactive vpn')
OLD = Path('/fixtures/old-version').read_text().strip()
NEW = Path('/fixtures/middle-version').read_text().strip()


class Session:
    def __init__(self):
        self.master, slave = pty.openpty()
        def terminal():
            # OpenRC boot scripts may inherit ignored terminal signals. Recreate
            # a normal terminal launch so Bash can install its Ctrl-C handler.
            for number in (signal.SIGINT, signal.SIGQUIT, signal.SIGHUP, signal.SIGTERM):
                signal.signal(number, signal.SIG_DFL)
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        self.process = subprocess.Popen(
            ['bash', '/fixtures/install.sh', '--download-base-url', 'http://127.0.0.1:18080'],
            stdin=slave, stdout=slave, stderr=slave, preexec_fn=terminal, close_fds=True)
        os.close(slave)
        self.buffer = b''
        self.transcript = b''

    def expect(self, text, answer=None, timeout=150):
        target = text.encode()
        deadline = time.monotonic() + timeout
        while target not in self.buffer:
            if time.monotonic() > deadline:
                raise AssertionError(f'Timeout waiting for {text!r}: {self.transcript.decode(errors="replace")}')
            if select.select([self.master], [], [], 1)[0]:
                try:
                    data = os.read(self.master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    data = b''
                if not data:
                    raise AssertionError(f'Exited before {text!r}: {self.transcript.decode(errors="replace")}')
                self.buffer += data
                self.transcript += data
        self.buffer = self.buffer.split(target, 1)[1]
        if answer is not None:
            os.write(self.master, answer.encode() + b'\n')

    def finish(self, code=0):
        deadline = time.monotonic() + 180
        while self.process.poll() is None and time.monotonic() < deadline:
            if select.select([self.master], [], [], 0.2)[0]:
                try:
                    data = os.read(self.master, 65536)
                    self.transcript += data
                except OSError:
                    pass
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGKILL)
            raise AssertionError('Installer did not exit: ' + self.transcript.decode(errors='replace'))
        os.close(self.master)
        assert self.process.returncode == code, self.transcript.decode(errors='replace')


def begin(language='', action=''):
    session = Session()
    session.expect('Language / 语言', language)
    session.expect('操作：' if language == '2' else 'Action:', action)
    return session


def uninstall(keep=True):
    session = begin('2', '3')
    session.expect('安装目录', str(ROOT))
    session.expect('保留配置、证书、数据库和日志', '' if keep else 'no')
    if not keep:
        session.expect('输入 DELETE', 'DELETE')
    session.expect('确认卸载', 'yes')
    session.finish()
    assert not (ROOT / 'ovpn').exists()
    assert not Path('/usr/local/bin/ovpn').is_symlink()


session = begin()
session.expect('Installation directory', str(ROOT))
session.expect('Release version', OLD)
session.expect('Public hostname or IP', '127.0.0.1')
session.expect('VPN IPv4 network', '')
session.expect('Transport protocol')
os.write(session.master, b'\x03')
session.finish(130)
assert not (ROOT / 'config/config.yaml').exists(), 'partial active configuration written'
assert (ROOT / 'ovpn').is_file(), 'interruption removed installed runtime'
state = Path('/var/lib/ovpn-go/installer/install')
assert state.stat().st_mode & 0o777 == 0o700
assert (state / 'endpoint').stat().st_mode & 0o777 == 0o600
session = begin()
session.expect('Transport protocol', '')  # resumes at the unanswered field
session.expect('VPN port', 'invalid')
session.expect('VPN port', '')
session.expect('Enable NAT', '')
session.expect('Route client Internet', '')
session.expect('DNS servers', '')
session.expect('Enable REST API', '')
session.expect('Advanced settings', '')
session.expect('Write this configuration', '')
session.expect('Enable service at boot')
os.write(session.master, b'\x03')
session.finish(130)
configuration_inode = (ROOT / 'config/config.yaml').stat().st_ino
config_path = ROOT / 'config/config.yaml'
config_path.write_bytes(b'# operator edit after configuration commit\n' + config_path.read_bytes())
session = begin()
session.expect('Enable service at boot', '')
session.expect('Start service now', '')
session.finish()
assert (ROOT / 'config/config.yaml').stat().st_ino == configuration_inode, 'resume rewrote active configuration'
subprocess.run([str(ROOT / 'ovpn'), 'runtime', 'health'], check=True)
config = (ROOT / 'config/config.yaml').read_bytes()
certificate = (ROOT / 'data/pki/ca.crt').read_bytes()
assert b'port: 1194' in config and b'network: "10.42.0.0/24"' in config
session = begin('2', '2')
session.expect('安装目录', str(ROOT))
session.expect('发布版本')
os.write(session.master, b'\x03')
session.finish(130)
session = begin('2', '2')
session.expect('发布版本', NEW)
session.expect('继续更新', '')
session.finish()
assert subprocess.check_output([str(ROOT / 'ovpn'), 'version', '--short']).decode().strip() == NEW
assert (ROOT / 'config/config.yaml').read_bytes() == config
assert (ROOT / 'data/pki/ca.crt').read_bytes() == certificate
# Enter at the uninstall confirmation must cancel without touching the live service.
session = begin('2', '3')
session.expect('安装目录', str(ROOT))
session.expect('保留配置、证书、数据库和日志', '')
session.expect('确认卸载', '')
session.finish()
subprocess.run([str(ROOT / 'ovpn'), 'runtime', 'health'], check=True)
uninstall()
assert (ROOT / 'data/pki/ca.crt').read_bytes() == certificate
session = begin()
session.expect('Installation directory', str(ROOT))
session.expect('Release version', NEW)
session.expect('Existing configuration is retained.')
session.expect('Enable service at boot', '')
session.expect('Start service now', '')
session.finish()
assert (ROOT / 'config/config.yaml').read_bytes() == config
assert (ROOT / 'data/pki/ca.crt').read_bytes() == certificate
uninstall(False)
assert not ROOT.exists()
# Exercise non-default advanced configuration and choosing not to start or enable.
session = begin('2', '')
session.expect('安装目录', str(ROOT))
session.expect('发布版本', NEW)
session.expect('公网域名或 IP', '127.0.0.1')
session.expect('VPN IPv4 网段', '')
session.expect('传输协议', 'tcp')
session.expect('VPN 端口', '1195')
session.expect('启用 NAT', 'yes')
session.expect('客户端互联网流量', 'yes')
session.expect('DNS 服务器', '1.1.1.1,8.8.8.8')
session.expect('启用 REST API', 'yes')
session.expect('API 监听 IP', '')
session.expect('API 端口', '')
session.expect('CORS 来源', '')
session.expect('高级设置', 'yes')
session.expect('传输地址族', 'ipv4')
session.expect('动态地址池大小', '32')
session.expect('允许客户端互通', 'no')
session.expect('NAT 网卡', '')
session.expect('推送路由', '192.0.2.0/24,198.51.100.0/24')
session.expect('日志轮转大小', '20971520')
session.expect('日志备份数量', '3')
session.expect('写入以上配置', 'no')
session.finish()
assert not (ROOT / 'config/config.yaml').exists()
session = begin('2', '')
session.expect('写入以上配置', '')
session.expect('开机自动启动', 'no')
session.expect('现在启动服务', 'no')
session.finish()
config = (ROOT / 'config/config.yaml').read_bytes()
for value in [b'protocol: "tcp"', b'port: 1195', b'dynamicPoolSize: 32',
              b'clientToClient: false', b'enabled: true', b'redirectGateway: true',
              b'dns: ["1.1.1.1", "8.8.8.8"]', b'"192.0.2.0/24"',
              b'"198.51.100.0/24"', b'maxBytes: 20971520', b'backups: 3']:
    assert value in config, config
systemd = Path('/run/systemd/system').exists()
control = ['systemctl'] if systemd else ['rc-service', 'ovpn']
def service(action):
    return subprocess.run(control + ([action, 'ovpn'] if systemd else [action]), check=True)
if systemd:
    assert subprocess.run(['systemctl', 'is-enabled', 'ovpn'], check=False).returncode != 0
else:
    assert not Path('/etc/runlevels/default/ovpn').exists()
assert subprocess.run([str(ROOT / 'ovpn'), 'runtime', 'health'], check=False).returncode != 0
# The isolated init fixtures only configure loopback. Give QEMU's actual
# virtual NIC its user-network address so NAT auto-detection has an egress route.
ip = str(ROOT / 'bin/ip')
if not subprocess.check_output([ip, '-4', 'route', 'show', 'default']).strip():
    nic = next(path.name for path in Path('/sys/class/net').iterdir() if path.name != 'lo')
    for args in [('link', 'set', 'dev', nic, 'up'),
                 ('address', 'replace', '10.0.2.15/24', 'dev', nic),
                 ('route', 'replace', 'default', 'via', '10.0.2.2', 'dev', nic)]:
        subprocess.run([ip, *args], check=True)
service('start')
for _ in range(60):
    if subprocess.run([str(ROOT / 'ovpn'), 'runtime', 'health'], stdout=subprocess.DEVNULL,
                      stderr=subprocess.DEVNULL, check=False).returncode == 0:
        break
    time.sleep(1)
else:
    raise AssertionError('Advanced configuration failed to start')
# Installer defaults keep API on loopback and CORS disabled; query the actual listener.
import urllib.request
import urllib.error
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
for _ in range(60):
    try:
        with opener.open('http://127.0.0.1:11940/healthz', timeout=2) as response:
            assert response.status == 200
        break
    except OSError:
        time.sleep(1)
else:
    raise AssertionError('Installer API configuration did not activate')
request = urllib.request.Request('http://127.0.0.1:11940/api/v1/state', method='OPTIONS',
                                 headers={'Origin': 'http://localhost:3000',
                                          'Access-Control-Request-Method': 'GET'})
try:
    opener.open(request, timeout=2)
    raise AssertionError('Empty installer CORS unexpectedly allowed localhost')
except urllib.error.HTTPError as error:
    assert error.code == 400
    assert error.headers.get('Access-Control-Allow-Origin') is None
assert b'openvpn-docker:' in subprocess.check_output([str(ROOT / 'bin/iptables-save')])
service('stop')
assert b'openvpn-docker:' not in subprocess.check_output([str(ROOT / 'bin/iptables-save')])
uninstall(False)
assert not ROOT.exists()
print('Interactive bilingual install/resume/update/uninstall: PASS', flush=True)
