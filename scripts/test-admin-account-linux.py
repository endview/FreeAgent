#!/usr/bin/env python3
"""Exercise an installed release's admin bootstrap and rollback on isolated Debian."""
import argparse
from contextlib import contextmanager
import grp
import http.client
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import threading
import time

spec = importlib.util.spec_from_file_location('public_acceptance', Path(__file__).with_name('test-public-web.py'))
web = importlib.util.module_from_spec(spec)
spec.loader.exec_module(web)
require = web.require


class SocketConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=20)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class Proxy(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_args):
        pass

    def forward(self):
        conn = SocketConnection(self.server.unix_path)
        try:
            body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
            fields = {key: value for key, value in self.headers.items() if key.lower() not in
                      ('connection', 'x-freeagent-key', 'x-freeagent-attach', 'x-freeagent-client-ip', 'x-freeagent-proxy-proto')}
            fields.update({'X-FreeAgent-Client-IP': '127.0.0.1', 'X-FreeAgent-Proxy-Proto': 'https'})
            conn.request(self.command, self.path, body or None, fields)
            res = conn.getresponse()
            data = res.read(5 << 20)
            self.send_response(res.status)
            for key, value in res.getheaders():
                if key.lower() not in ('content-length', 'transfer-encoding', 'connection'):
                    self.send_header(key, value)
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except OSError:
            self.send_error(502)
        finally:
            conn.close()

    do_GET = do_POST = do_PUT = do_PATCH = forward


class Model(web.ProtocolFixture):
    def do_GET(self):
        raw = b'{"data":[{"id":"fixture-model","object":"model"}]}'
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--engine', type=Path, required=True)
    parser.add_argument('--legacy', type=Path, action='append', default=[])
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--socket-directory', type=Path, required=True)
    args = parser.parse_args()
    require(os.geteuid() != 0 and not args.root.exists(), 'Use a new test root and a non-root service user')
    require(args.socket_directory.parent == Path('/run') and args.socket_directory.name.startswith('freeagent-admin-test-'),
            'A dedicated /run/freeagent-admin-test-* directory is required')
    require(args.socket_directory.stat().st_uid == os.geteuid(), 'Socket directory must be owned by this test user')
    args.root.mkdir(mode=0o700)
    checks = {}
    env = dict(os.environ, XDG_CONFIG_HOME=str(args.root / 'config'), XDG_CACHE_HOME=str(args.root / 'cache'),
               FREEAGENT_KEYS_FILE=str(args.root / 'isolated-keys.json'))
    cert, key = args.root / 'tls.crt', args.root / 'tls.key'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-days', '1', '-nodes',
                    '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1', '-out', str(cert), '-keyout', str(key)],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    trust = ssl.create_default_context(cafile=str(cert))
    proxy = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
    proxy.socket = tls.wrap_socket(proxy.socket, server_side=True)
    proxy.unix_path = str(args.socket_directory / 'web.sock')
    origin = f'https://127.0.0.1:{proxy.server_port}'
    provider = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Model)
    for server in (proxy, provider):
        threading.Thread(target=server.serve_forever, daemon=True).start()

    def cli(engine, data, *command, input_text=None, expect=True):
        result = subprocess.run([str(engine), *command, '--data-dir', str(data)], cwd=args.root,
                                input=input_text, text=True, env=env, capture_output=True, timeout=45)
        require((result.returncode == 0) == expect, f'CLI {"/".join(command[:2])} failed, exit={result.returncode}')
        return json.loads(result.stdout) if expect and result.stdout.startswith('{') else None

    def init(name):
        data, work = args.root / name, args.root / f'{name}-work'
        work.mkdir()
        cli(args.engine, data, 'init', '--workspace', str(work))
        return data

    def configure(data):
        cli(args.engine, data, 'public', 'enable', '--origin', origin, '--socket', proxy.unix_path,
            '--socket-group', grp.getgrgid(os.getegid()).gr_name, input_text='y\n')

    @contextmanager
    def owner(engine, data):
        with (args.root / (data.name + '-owner.log')).open('ab') as log:
            process = subprocess.Popen([str(engine), 'web', '--data-dir', str(data), '--port', '0'], env=env, cwd=args.root,
                                       stdin=subprocess.DEVNULL, stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    probe = web.Client(origin, trust)
                    if probe.request('GET', '/api/public/session')[0] == 200:
                        break
                    require(process.poll() is None, 'Isolated Owner exited; see private log')
                    time.sleep(.1)
                else:
                    raise RuntimeError('Isolated HTTPS entrance did not start')
                yield
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=20)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=10)

    def authenticate(password, username, expected=200):
        client = web.Client(origin, trust)
        status, state, _, _ = client.request('GET', '/api/public/session')
        require(status == 200 and not state['authenticated'], 'Fresh HTTPS session unavailable')
        args = {'password': password}
        if username is not None:
            args['username'] = username
        status, state, _, headers = client.request('POST', '/api/public/login', args)
        require(status == expected, f'Login status {status}; expected {expected}')
        if expected == 200:
            client.csrf = state['csrf']
            cookies = ' '.join(headers.get_all('Set-Cookie')).lower()
            require(all(flag in cookies for flag in ('httponly', 'secure', 'samesite=strict')), 'Unsafe cookie')
        return client

    def api(client, method, path, value=None, expected=200):
        status, payload, _, _ = client.request(method, '/api/' + path, value)
        require(status == expected, f'{method} {path} returned {status}; expected {expected}')
        return payload

    try:
        pw = secrets.token_hex(24)
        data = init('fresh')
        cli(args.engine, data, 'admin', 'init', '--username', 'isolated-admin', '--password-stdin', input_text=pw+'\n')
        cli(args.engine, data, 'admin', 'init', '--username', 'second', '--password-stdin', input_text=pw+'\n', expect=False)
        configure(data)
        with owner(args.engine, data):
            client = authenticate(pw, 'isolated-admin')
            authenticate(pw, 'admin', 401)
            api(client, 'GET', 'admin-account', expected=404)
            secret = secrets.token_hex(18)
            profile = api(client, 'POST', 'keys', {'name': 'isolated model', 'source': 'saved',
                'base_url': f'http://127.0.0.1:{provider.server_port}/v1', 'protocol': 'openai-compatible', 'key': secret}, 201)
            require(secret not in json.dumps(profile), 'Model key echoed')
            api(client, 'PUT', 'providers', {'id': profile['id'], 'enabled': True})
            probe = api(client, 'POST', 'keys/test', {'id': profile['id']})
            require(probe['category'] == 'ok' and 'fixture-model' in probe['models'], 'Model probe failed')
            api(client, 'POST', 'models/enable', {'key': profile['id'], 'model': 'fixture-model', 'default': True})
            chat = client.chat('isolated first login')
            client.wait(chat, client.submit(chat, 'hello isolated fixture'))
            token = '123456789:' + secrets.token_hex(20)
            api(client, 'POST', 'channels/credentials', {'name': 'isolated unconnected Bot', 'source': 'saved',
                'base_url': 'https://api.telegram.org', 'protocol': 'telegram', 'secret': token})
            refs = api(client, 'GET', 'channels/credentials')
            require(len(refs) == 1 and token not in json.dumps(refs), 'Token readback unsafe')
            settings = api(client, 'GET', 'public-settings')
            require(settings['config']['max_permission'] == 'ask', 'Unsafe bootstrap ceiling')
            proposed = dict(settings['config'], max_permission='full', expected_revisions=settings['expected_revisions'])
            status, response, _, _ = client.request('PUT', '/api/public-settings', proposed)
            require(status >= 400 and response['code'] == 'local_confirmation_required', 'Public setting escaped local approval')
            match = re.search(r'control confirm --operation ([a-f0-9]{32})', response['error'])
            require(match, 'Original proposal reference missing')
            operation = match[1]
            record = cli(args.engine, data, 'control', 'inspect', '--operation', operation)
            require(record['operation_id'] == operation and record['local_approval_required'] and record['status'] == 'pending', 'CLI did not inspect original proposal')
            cli(args.engine, data, 'control', 'confirm', '--operation', operation, input_text='y\n')
            require(api(client, 'GET', 'public-settings')['config']['max_permission'] == 'full', 'CLI approval was not saved')
            cli(args.engine, data, 'control', 'confirm', '--operation', operation, input_text='y\n', expect=False)
            recovered = secrets.token_hex(24)
            cli(args.engine, data, 'admin', 'reset-password', '--password-stdin', input_text=recovered+'\n')
            api(client, 'GET', 'public/account', expected=401)
            authenticate(recovered, 'isolated-admin')
        checks['fresh_cli_bootstrap_https_mock_model_write_only_token_local_approval'] = True
        for index, previous in enumerate(args.legacy):
            version = subprocess.check_output([str(previous), '--version'], text=True).split()[1].split('=')[-1]
            data = init(f'rollback-{index}')
            one, two, three = (secrets.token_hex(24) for _ in range(3))
            cli(previous, data, 'public', 'password', '--password-stdin', input_text=one+'\n')
            configure(data)
            with owner(args.engine, data):
                client = authenticate(one, 'admin')
                initial = api(client, 'GET', 'public/account')['account']
                api(client, 'PUT', 'public/account', {'username': 'carry-forward'})
                client = authenticate(one, 'carry-forward')
                api(client, 'POST', 'public/password', {'current': one, 'password': two})
                api(client, 'GET', 'public/account', expected=401)
            with owner(previous, data):
                authenticate(one, None, 401)
                old_client = authenticate(two, None)
                api(old_client, 'POST', 'public/password', {'current': two, 'password': three})
                authenticate(three, None)
            with owner(args.engine, data):
                authenticate(two, 'carry-forward', 401)
                client = authenticate(three, 'carry-forward')
                current = api(client, 'GET', 'public/account')['account']
                require(initial['id'] == current['id'] and current['username'] == 'carry-forward', 'Account identity lost on rollback')
            checks['v1_round_trip_' + version] = True
    finally:
        for server in (provider, proxy):
            server.shutdown()
            server.server_close()
    print(json.dumps({'ok': True, 'checks': checks, 'real_provider': 'NOT_RUN', 'real_telegram_send': 'NOT_RUN'}))


if __name__ == '__main__':
    main()
