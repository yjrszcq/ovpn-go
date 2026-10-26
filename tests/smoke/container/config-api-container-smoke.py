#!/usr/bin/env python3
"""Verify real editor/apply and API listener activation in an isolated container."""
import json, os, re, subprocess, tempfile, time, urllib.request, urllib.error, sys
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
    docker('run', '-d', '--name', name, '--cap-add', 'NET_ADMIN', '--device', '/dev/net/tun', '-v', str(root / 'config') + ':/etc/ovpn-conf', '-v', str(root / 'data') + ':/etc/openvpn', '-e', 'OVPN_API_LISTEN=0.0.0.0:11940', '-e', 'OVPN_API_CORS_ORIGINS=*', '-p', '127.0.0.1::11940', '-p', '127.0.0.1::11941', image, 'server', 'run')
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

    # Recover the actual file while OpenVPN is running, without a configuration apply.
    applied_before = json.loads(request('/api/v1/config/applied')[2])
    revision = applied_before['revision']
    config_path = '/etc/ovpn-conf/config.yaml'

    def processes():
        # Musl payloads run through their private loader; pidof is unreliable here.
        return docker('top', name, '-eo', 'pid,args').splitlines()[1:]

    processes_before = processes()
    status, headers, exported = request('/api/v1/config/export')
    assert status == 200 and headers['Content-Type'] == 'application/yaml'
    assert headers['Content-Disposition'] == 'attachment; filename="config.yaml"'
    assert headers['Cache-Control'] == 'no-store'
    assert b'endpoint: 127.0.0.1' in exported
    restore_body = {'current_revision': revision, 'confirm': True}
    for damaged in ['valid', 'invalid', 'missing']:
        if damaged == 'invalid':
            docker('exec', name, 'sh', '-c', 'printf "invalid: [" > ' + config_path)
        elif damaged == 'missing':
            docker('exec', name, 'sh', '-c', 'unlink ' + config_path)
        else:
            docker('exec', name, 'sh', '-c', "sed -i 's/endpoint: 127.0.0.1/endpoint: draft.example.test/' " + config_path + ' && printf "# unapplied comment\n" >> ' + config_path)
        # Export always reads applied state, even if desired YAML cannot be parsed.
        assert request('/api/v1/config/export')[2] == exported
        status, headers, body = request('/api/v1/config/restore', 'POST', restore_body)
        assert status == 200, (status, body)
        assert json.loads(body) == applied_before
        assert headers['ETag'] == '"' + applied_before['digest'] + '"'
        assert docker('exec', name, 'cat', config_path).encode() == exported
        assert docker('exec', name, 'stat', '-c', '%a', config_path).strip() == '600'
    for data, expected in [
        ({'current_revision': revision + 1, 'confirm': True}, 409),
        ({'current_revision': revision}, 422),
        ({'confirm': True}, 422),
        ({'current_revision': revision, 'confirm': True, 'force': True}, 400),
    ]:
        status, _, body = request('/api/v1/config/restore', 'POST', data)
        assert status == expected, (status, body)
        assert docker('exec', name, 'cat', config_path).encode() == exported
    for path, method, expected in [
        ('/api/v1/config/export?unexpected=1', 'GET', 400),
        ('/api/v1/config/export', 'POST', 405),
        ('/api/v1/config/restore', 'GET', 405),
    ]:
        assert request(path, method)[0] == expected
    for path, method in [('/api/v1/config/export', 'GET'), ('/api/v1/config/restore', 'POST')]:
        unauthenticated = urllib.request.Request(base + path, method=method)
        try:
            urllib.request.urlopen(unauthenticated, timeout=10)
            raise AssertionError('Unauthenticated configuration request accepted')
        except urllib.error.HTTPError as error:
            assert error.code == 401
    docker('exec', name, 'sh', '-c', 'mv ' + config_path + ' /etc/ovpn-conf/saved.yaml && ln -s /etc/ovpn-conf/saved.yaml ' + config_path)
    status, _, body = request('/api/v1/config/restore', 'POST', restore_body)
    assert status == 422 and json.loads(body)['error']['kind'] == 'unsafe_config_target'
    assert docker('exec', name, 'cat', '/etc/ovpn-conf/saved.yaml').encode() == exported
    docker('exec', name, 'sh', '-c', 'unlink ' + config_path + ' && mv /etc/ovpn-conf/saved.yaml ' + config_path)
    lock = subprocess.Popen(['docker', 'exec', name, 'flock', '-x', '/etc/openvpn/.ovpn-data.lock', 'sh', '-c', 'echo locked; sleep 4'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        assert lock.stdout.readline().strip() == b'locked'
        status, _, body = request('/api/v1/config/restore', 'POST', restore_body)
        assert status == 409, (status, body)
    finally:
        assert lock.wait(timeout=10) == 0
    assert request('/api/v1/config/restore', 'POST', restore_body)[0] == 200
    assert json.loads(request('/api/v1/config/applied')[2]) == applied_before
    assert json.loads(cli('config', 'plan', '-j'))['configuration']['in_sync'] is True
    assert processes() == processes_before, 'Restoration restarted a managed process'
    cli('runtime', 'health')

    # API listener settings do not exist in the REST write model.
    desired = json.loads(request('/api/v1/config/desired')[2])
    for change in [{'enabled': False}, {'listen_ip': '127.0.0.1'}, {'port': 11941}, {'cors': ['*']}]:
        body = dict(desired['config'])
        body['api'] = dict(desired['config']['api'], **change)
        status, _, _ = request('/api/v1/config/desired', 'PUT', body, {'If-Match': '"' + desired['digest'] + '"'})
        assert status == 400
        assert json.loads(request('/api/v1/config/desired')[2]) == desired
    # Ordinary VPN changes omit api and preserve all local listener settings.
    vpn = dict(desired['config'])
    del vpn['api']
    vpn['logging'] = dict(vpn['logging'], backups=3)
    status, _, body = request('/api/v1/config/desired', 'PUT', vpn, {'If-Match': '"' + desired['digest'] + '"'})
    assert status == 200, (status, body)
    updated = json.loads(body)
    assert updated['config']['api'] == desired['config']['api']
    status, _, body = request('/api/v1/config/apply', 'POST', {'desired_digest': updated['digest'], 'current_revision': revision, 'force': False})
    assert status == 200 and json.loads(body)['applied'] is True, (status, body)
    wait(lambda: cli('runtime', 'health'))

    def stage_local_api(change):
        settings = json.loads(cli('config', 'show', '-j'))['config']['api']
        settings.update(change)
        settings['listenIP'] = settings.pop('listen_ip')
        content = re.sub(r'^api:.*?(?=^\S|\Z)', '', cli('config', 'export'), flags=re.M | re.S)
        content = 'api: ' + json.dumps(settings) + '\n' + content
        subprocess.run(['docker', 'exec', '-i', name, 'sh', '-c', 'cat > /etc/ovpn-conf/config.yaml'], input=content.encode(), check=True)

    stage_local_api({'port': 11941, 'cors': ['console.example:3000']})
    desired = json.loads(request('/api/v1/config/desired')[2])
    applied = json.loads(request('/api/v1/config/applied')[2])
    unchanged_pids = processes()
    for force in [False, True]:
        status, _, body = request('/api/v1/config/apply', 'POST', {'desired_digest': desired['digest'], 'current_revision': applied['revision'], 'force': force})
        assert status == 422 and json.loads(body)['error']['kind'] == 'local_configuration_required', (status, body)
    assert json.loads(request('/api/v1/config/applied')[2]) == applied
    assert processes() == unchanged_pids
    cli('config', 'apply', '--yes')
    wait(lambda: request('/healthz', address=newbase)[0] == 200)
    status, _, _ = request('/api/v1/state', 'OPTIONS', headers={'Origin': 'https://console.example:3000', 'Access-Control-Request-Method': 'GET'}, address=newbase)
    assert status == 204
    stage_local_api({'enabled': False})
    cli('config', 'apply', '--yes')

    def closed():
        try:
            request('/healthz', address=newbase)
            return False
        except (urllib.error.URLError, ConnectionError):
            return True
    wait(closed)
    cli('runtime', 'health')
    stage_local_api({'enabled': True})
    cli('config', 'edit', '--editor', '/etc/ovpn-conf/editor', '--apply', '--yes')
    wait(lambda: request('/healthz', address=newbase)[0] == 200)
    cli('runtime', 'health')
    print('Actual container: YAML export/restore (valid/missing/malformed, auth, stale revision, unsafe target, concurrent lock, unchanged applied state/PIDs), editor+apply, ignored legacy API environment, disabled empty CORS, REST write model excludes API settings, VPN apply preserves listener, local CLI port/CORS update and disable/re-enable: PASS')
except BaseException:
    print(docker('logs', name))
    raise
finally:
    subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['docker', 'run', '--rm', '-v', str(root) + ':/work', '--entrypoint', 'sh', image, '-ec', 'rm -rf /work/*'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    root.rmdir()
