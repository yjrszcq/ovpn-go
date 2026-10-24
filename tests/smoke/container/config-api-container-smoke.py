#!/usr/bin/env python3
"""Verify real editor/apply and API listener activation in an isolated container."""
import json, os, subprocess, tempfile, time, urllib.request, urllib.error, sys
from pathlib import Path
urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))
image = sys.argv[1] if len(sys.argv) > 1 else 'ovpn-config-api:amd64'
root = Path(tempfile.mkdtemp(prefix='ovpn-api-activation-'))
(root / 'config').mkdir()
(root / 'data').mkdir()
(root / 'config/config.yaml').write_text('version: 1\nserver: {endpoint: 127.0.0.1}\nipv4: {network: 10.42.0.0/24}\napi: {enabled: false, listenIP: 0.0.0.0, port: 11940, cors: []}\n')
(root / "config/editor").write_text("#!/bin/sh\nsed -i '/^api:/,/^[^ ]/s/enabled: false/enabled: true/' \"$1\"\n")
(root / 'config/editor').chmod(493)
name = 'ovpn-api-activation-' + str(os.getpid())

def docker(*args):
    return subprocess.check_output(['docker', *args], stderr=subprocess.STDOUT).decode()

def cli(*args):
    return docker('exec', name, 'ovpn', *args)

def wait(check):
    for _ in range(120):
        try:
            value = check()
            if value:
                return value
        except (urllib.error.URLError, subprocess.CalledProcessError, ConnectionError):
            pass
        time.sleep(0.25)
    raise AssertionError('Readiness timeout')
try:
    docker('run', '-d', '--name', name, '--cap-add', 'NET_ADMIN', '--device', '/dev/net/tun', '-v', str(root / 'config') + ':/etc/ovpn-conf', '-v', str(root / 'data') + ':/etc/openvpn', '-e', 'OVPN_API_LISTEN=0.0.0.0:11940', '-e', 'OVPN_API_CORS_ORIGINS=*', '-p', '127.0.0.1::11940', '-p', '127.0.0.1::11941', image)
    wait(lambda: cli('runtime', 'health'))
    ports = [int(docker('port', name, f'{port}/tcp').strip().rsplit(':', 1)[1]) for port in [11940, 11941]]
    base = 'http://127.0.0.1:' + str(ports[0])
    newbase = 'http://127.0.0.1:' + str(ports[1])

    def request(path, method='GET', data=None, headers=None, address=None):
        h = dict(headers or {})
        h['Content-Type'] = 'application/json'
        if 'key' in globals():
            h['Authorization'] = 'Bearer ' + key
        req = urllib.request.Request((address or base) + path, data=None if data is None else json.dumps(data).encode(), headers=h, method=method)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                return (response.status, response.headers, response.read())
        except urllib.error.HTTPError as error:
            return (error.code, error.headers, error.read())
    try:
        request('/healthz')
        raise AssertionError('Legacy environment enabled a disabled API')
    except (urllib.error.URLError, ConnectionError):
        pass
    key = json.loads(cli('api', 'key', 'create', 'activation-smoke', '--json'))['secret']
    initial_revision = json.loads(cli('config', 'show', '-j'))['revision']
    cli('config', 'edit', '-e', '/etc/ovpn-conf/editor', '-p')
    assert json.loads(cli('config', 'show', '-j'))['revision'] == initial_revision
    cli('config', 'edit', '-e', '/etc/ovpn-conf/editor', '-a', '-y')
    wait(lambda: request('/healthz')[0] == 200)
    for origin, expected in [('http://localhost:3000', 400), ('https://127.0.0.1:8080', 400), ('https://console.example', 400)]:
        code, _, body = request('/api/v1/state', 'OPTIONS', headers={'Origin': origin, 'Access-Control-Request-Method': 'GET'})
        assert code == expected, (origin, code, body)

    def apply_api(change, address=None):
        desired = json.loads(request('/api/v1/config/desired', address=address)[2])
        applied = json.loads(request('/api/v1/config/applied', address=address)[2])
        desired['config']['api'].update(change)
        status, _, body = request('/api/v1/config/desired', 'PUT', desired['config'], {'If-Match': '"' + desired['digest'] + '"'}, address)
        assert status == 200, (status, body)
        updated = json.loads(body)
        status, _, body = request('/api/v1/config/apply', 'POST', {'desired_digest': updated['digest'], 'current_revision': applied['revision'], 'force': False}, address=address)
        assert status == 200, (status, body)
        assert json.loads(body)['applied'] is True
    apply_api({'port': 11941, 'cors': ['console.example:3000']})
    wait(lambda: request('/healthz', address=newbase)[0] == 200)
    status, _, _ = request('/api/v1/state', 'OPTIONS', headers={'Origin': 'https://console.example:3000', 'Access-Control-Request-Method': 'GET'}, address=newbase)
    assert status == 204
    apply_api({'enabled': False}, newbase)

    def closed():
        try:
            request('/healthz', address=newbase)
            return False
        except (urllib.error.URLError, ConnectionError):
            return True
    wait(closed)
    cli('runtime', 'health')
    cli('config', 'edit', '--editor', '/etc/ovpn-conf/editor', '--apply', '--yes')
    wait(lambda: request('/healthz', address=newbase)[0] == 200)
    cli('runtime', 'health')
    print('Actual container: editor+apply, ignored legacy API environment, disabled empty CORS, REST port/CORS update, REST disable response, CLI re-enable: PASS')
except BaseException:
    print(docker('logs', name))
    raise
finally:
    subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['docker', 'run', '--rm', '-v', str(root) + ':/work', '--entrypoint', 'sh', image, '-ec', 'rm -rf /work/*'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    root.rmdir()
