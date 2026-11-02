#!/usr/bin/env python3
"""Offline Linux portable acceptance; run as an ordinary user in a new directory."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path, PurePosixPath
import selectors
import shlex
import shutil
import signal
import socket
import stat
import struct
import subprocess
import tarfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def wait_for(call, message, seconds=15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = call()
        if result:
            return result
        time.sleep(.025)
    raise RuntimeError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', required=True)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--root', required=True)
    args = parser.parse_args()
    require(os.getuid() != 0, 'Acceptance must run as a non-root user')
    root, archive = Path(args.root).resolve(), Path(args.archive).resolve()
    require(not root.exists(), 'Acceptance root must be new')
    require(hashlib.sha256(archive.read_bytes()).hexdigest() == args.sha256, 'Archive checksum mismatch')
    root.mkdir(mode=0o700)
    with tarfile.open(archive, 'r:gz') as tar:
        seen, total = set(), 0
        for member in tar:
            name = PurePosixPath(member.name)
            require(not name.is_absolute() and '..' not in name.parts and '\\' not in member.name,
                    'Unsafe archive path')
            require(member.name not in seen, 'Repeated archive member')
            seen.add(member.name)
            destination = root.joinpath(*name.parts)
            require(destination.resolve().is_relative_to(root), 'Archive path escaped test root')
            require(member.isdir() or member.isfile(), 'Archive has links or special files')
            if member.isdir():
                destination.mkdir(parents=True, exist_ok=True)
            else:
                total += member.size
                require(total < 256 << 20, 'Archive payload exceeds bound')
                destination.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, destination.open('xb') as target:
                    shutil.copyfileobj(source, target)
            destination.chmod(member.mode & 0o777)
    packages = [p for p in root.iterdir() if p.is_dir()]
    require(len(packages) == 1, 'Expected one portable package')
    package = packages[0]
    manifest = json.loads((package / 'manifest.json').read_text())
    require(manifest['target'] == 'linux/amd64' and manifest['data_format'] == 'Harness-v7', 'Wrong target/format')
    require(not manifest['bundled_credentials'] and not manifest['bundled_user_data'], 'Bundled state claimed')
    require(not (package / 'data').exists() and not (package / 'workspace').exists(), 'Package contains runtime state')
    for line in (package / 'SHA256SUMS.txt').read_text().splitlines():
        expected, name = line.split('  ', 1)
        require(hashlib.sha256((package / name).read_bytes()).hexdigest() == expected, 'Payload checksum mismatch')
    cli, launcher = package / 'bin/freeagent', package / 'FreeAgent'
    require(os.access(cli, os.X_OK) and os.access(launcher, os.X_OK), 'Unix execute bits missing')
    require(b'\r' not in launcher.read_bytes(), 'Launcher has CRLF')
    elf = cli.read_bytes()
    require(elf[:5] == b'\x7fELF\x02' and struct.unpack_from('<H', elf, 18)[0] == 62, 'Not an amd64 ELF')
    offset = struct.unpack_from('<Q', elf, 32)[0]
    size, count = struct.unpack_from('<HH', elf, 54)
    require(all(struct.unpack_from('<I', elf, offset + i * size)[0] != 3 for i in range(count)),
            'Binary unexpectedly requires a dynamic interpreter')

    env = dict(os.environ)
    env.pop('FREEAGENT_KEYS_FILE', None)
    env['XDG_CONFIG_HOME'] = str(root / 'fallback-config')
    env['XDG_CACHE_HOME'] = str(root / 'fallback-cache')
    env['LANG'] = 'C.UTF-8'
    checks, uncertainty = {}, []

    def run(*arguments, input_text=None, success=True, parse=True):
        process = subprocess.run([str(cli), *arguments], input=input_text, text=True,
                                 encoding='utf-8', capture_output=True, timeout=35,
                                 cwd=root, env=env)
        if success:
            require(process.returncode == 0, f'CLI {arguments[0]} failed: {process.stderr[:1200]}')
        if parse and process.returncode == 0:
            return json.loads(process.stdout)
        return process

    checks['version'] = run('--version', parse=False).stdout.strip() == manifest['build_info']
    require(checks['version'], 'Manifest and executable versions differ')
    help_result = subprocess.run([str(launcher), 'web', '--help'], env=env, cwd=root,
                                 text=True, capture_output=True, timeout=10)
    require('Usage' in help_result.stderr and not (package / 'data').exists() and
            not (package / 'workspace').exists(), 'Launcher help initialized state')
    invalid = subprocess.run([str(launcher), 'web', '--data-dir'], env=env, cwd=root,
                             text=True, capture_output=True, timeout=10)
    require(invalid.returncode != 0 and not (package / 'data').exists() and
            not (package / 'workspace').exists(), 'Invalid launcher arguments initialized state')
    checks['help_and_invalid_arguments_leave_no_state'] = True
    error_log = (root / 'owner.stderr').open('w+', encoding='utf-8')
    owner = None
    fixture = None
    fixture_release = threading.Event()
    fixture_calls = []
    fixture_lock = threading.Lock()
    origin, headers = '', {}

    def api(method, path, body=None, expected=200):
        payload = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(origin + path, payload, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        require(status == expected, f'HTTP {method} {path} returned {status}, expected {expected}')
        return json.loads(raw) if raw else None

    def start_owner():
        nonlocal owner, origin, headers
        owner = subprocess.Popen([str(launcher)], cwd=root, env=env, stdin=subprocess.PIPE,
                                 stdout=subprocess.PIPE, stderr=error_log, text=True,
                                 encoding='utf-8', start_new_session=True)
        with selectors.DefaultSelector() as selector:
            selector.register(owner.stdout, selectors.EVENT_READ)
            require(bool(selector.select(15)), 'Launcher did not print a loopback URL')
            address = urllib.parse.urlsplit(owner.stdout.readline().strip())
        require(address.scheme == 'http' and address.hostname == '127.0.0.1' and address.port,
                'Owner listener is not loopback')
        capability = urllib.parse.parse_qs(address.fragment).get('key', [''])[0]
        require(len(capability) == 64, 'Missing local interactive capability')
        origin = f'http://127.0.0.1:{address.port}'
        headers = {'Origin': origin, 'X-FreeAgent-Key': capability, 'Content-Type': 'application/json'}
        return address.port

    def stop_owner():
        nonlocal owner
        if owner and owner.poll() is None:
            owner.send_signal(signal.SIGTERM)
            owner.wait(timeout=15)
            require(owner.returncode == 0, 'SIGTERM did not shut down gracefully')
        owner = None

    def settled(chat):
        snapshot = api('GET', '/api/chats/' + chat)
        return snapshot if not snapshot['busy'] and snapshot['view']['turns'] else None

    def new_chat(title):
        return api('POST', '/api/chats', {'title': title}, expected=201)['id']

    try:
        port = start_owner()
        data, workspace = package / 'data/Harness-v7', package / 'workspace'
        config = json.loads((data / 'config.json').read_text())
        require(config['workspace'] == str(workspace) and config['models'] == {}, 'Fresh init is not isolated/empty')
        require(all(not c['enabled'] for c in config['channels'].values()), 'Channel enabled by default')
        checks['fresh_launcher'] = True
        with urllib.request.urlopen(origin + '/', timeout=5) as page:
            require(page.status == 200 and b'<html' in page.read().lower(), 'Embedded Web UI not served')
        checks['embedded_web'] = True
        run('config', 'model', '--id', 'echo', '--provider', 'echo', input_text='y\n')
        chat = new_chat('Debian portable continuation')
        api('POST', '/api/chats/' + chat + '/submit', {'prompt': 'from web', 'request_key': 'web-first'}, 202)
        snapshot = wait_for(lambda: settled(chat), 'Web Echo did not finish')
        turn = snapshot['view']['turns'][-1]
        require(turn['state'] == 'completed' and turn['messages'][1]['raw'] == 'Echo: from web', 'Wrong web reply')
        require(turn['permission']['mode'] == 'none', 'Fresh space did not retain None')
        checks['none_chat'] = True
        continued = run('run', '--session', chat, '--request', 'cli-second', '--json', 'from CLI')
        require(continued['answer'] == 'Echo: from CLI', 'CLI did not continue the same conversation')
        duplicate = run('run', '--session', chat, '--request', 'cli-second', '--json', 'from CLI')
        require(duplicate['duplicate'] and duplicate['run']['id'] == continued['run']['id'], 'CLI request replayed')
        view = api('GET', '/api/chats/' + chat)['view']
        require(len(view['turns']) == 2 and view['turns'][-1]['messages'][-1]['raw'] == 'Echo: from CLI',
                'Web did not see CLI records')
        checks['cli_web_attach_and_dedup'] = True

        sentinel = 'FA_PORTABLE_SYNTHETIC_KEY_NOT_A_CREDENTIAL'
        key = run('key', 'add', '--name', 'portable-sentinel', '--url', 'https://example.invalid',
                  '--key-stdin', input_text=sentinel + '\n')
        keys_path = package / 'data/keys.json'
        require(stat.S_IMODE(keys_path.stat().st_mode) == 0o600, 'Saved keys are not mode 0600')
        require(not key['encrypted'] and key['protection'] == 'plaintext', 'Linux key protection is misreported')
        doctor = run('doctor')
        require(doctor['keys_file'] == str(keys_path) and sentinel not in json.dumps(doctor), 'Wrong/leaked key metadata')
        run('key', 'remove', 'portable-sentinel', '--force')
        checks['portable_key_path_and_0600'] = True

        class Provider(http.server.BaseHTTPRequestHandler):
            def log_message(self, *unused):
                pass

            def do_POST(self):
                length = int(self.headers.get('Content-Length', '0'))
                body = json.loads(self.rfile.read(length))
                prompt = body['messages'][-1]['content']
                with fixture_lock:
                    fixture_calls.append(prompt)
                fixture_release.wait(12)
                response = {'id': 'fixture', 'object': 'chat.completion', 'model': 'fixture',
                            'choices': [{'index': 0, 'finish_reason': 'stop',
                                         'message': {'role': 'assistant', 'content': 'fixture ' + prompt}}],
                            'usage': {'prompt_tokens': 5, 'completion_tokens': 5, 'total_tokens': 10}}
                raw = json.dumps(response).encode()
                try:
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        fixture = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        threading.Thread(target=fixture.serve_forever, daemon=True).start()
        run('key', 'add', '--name', 'loopback-fixture', '--url', f'http://127.0.0.1:{fixture.server_port}/v1', '--no-key', '--enable', input_text='y\n')
        run('config', 'model', '--id', 'fixture', '--key', 'loopback-fixture', '--model', 'fixture', '--default',
            input_text='y\n')
        a, b, c = (new_chat('parallel ' + name) for name in ('A', 'B', 'C'))
        for current, text in ((a, 'A'), (b, 'B')):
            api('POST', f'/api/chats/{current}/submit', {'prompt': text, 'request_key': text}, 202)
        wait_for(lambda: len(fixture_calls) == 2, 'Two model requests did not overlap')
        state = api('GET', '/api/attach/state')
        require(set(state['active_chats']) == {a, b}, 'Owner does not track both conversations')
        api('POST', f'/api/chats/{c}/submit', {'prompt': 'third', 'request_key': 'third'}, 409)
        api('POST', f'/api/chats/{a}/submit', {'prompt': 'same', 'request_key': 'same'}, 409)
        api('POST', f'/api/chats/{a}/stop', {})
        stopped = wait_for(lambda: settled(a), 'Stopped A did not settle')
        require(stopped['view']['turns'][-1]['state'] == 'unknown', 'Interrupted dispatch lost UNKNOWN')
        require(api('GET', f'/api/chats/{b}')['busy'], 'Stopping A stopped B')
        fixture_release.set()
        finished = wait_for(lambda: settled(b), 'B did not finish')
        require(finished['view']['turns'][-1]['state'] == 'completed' and len(fixture_calls) == 2,
                'Parallel execution replayed or crossed replies')
        unknown_run = stopped['view']['turns'][-1]['key']
        refused = run('continue', '--run', unknown_run, '--json', success=False)
        require(refused.returncode != 0 and len(fixture_calls) == 2, 'UNKNOWN was replayed')
        api('POST', f'/api/chats/{a}/end', {'acknowledge': True})
        checks['two_foreground_scoped_stop_and_unknown'] = True
        run('config', 'model', '--id', 'echo', '--default', input_text='y\n')

        shell = str(Path(shutil.which('sh')).resolve())
        run('config', 'commands', '--enabled', '--allow', shell, '--timeout', '20', '--output-bytes', '4096',
            input_text='y\n')
        api('PUT', '/api/commands/rules', {'rules': [{'program': shell, 'decision': 'allow'}]})
        run('spaces', 'permission', '--space', 'local', '--mode', 'workspace', '--yes')
        arguments = {'command': shell, 'args': ['-c', 'printf denied > sandbox-denied.txt'], 'cwd': '.'}
        refused = run('commands', 'start', '--session', chat, '--arguments', json.dumps(arguments), '--json',
                      success=False)
        require(refused.returncode != 0 and not (workspace / 'sandbox-denied.txt').exists(),
                'Linux sandbox request fell back to host execution')
        denied_result = json.loads(refused.stdout)
        require(any(receipt['status'] == 'unsupported' and not receipt.get('host_escape_hatch', False)
                    for receipt in denied_result.get('evidence', {}).get('commands_run', [])),
                'Sandbox refusal lacks an explicit unsupported runtime receipt')
        if denied_result['run']['status'] not in ('completed', 'failed', 'abandoned'):
            api('POST', f'/api/chats/{chat}/end', {'acknowledge': True})
        checks['sandbox_refusal_without_host_fallback'] = True
        run('spaces', 'permission', '--space', 'local', '--mode', 'full', '--yes')
        arguments['args'] = ['-c', 'printf command-proof > portable-command.txt; printf command-ok']
        command = run('commands', 'start', '--session', chat, '--arguments', json.dumps(arguments), '--json')
        require(command['run']['status'] == 'completed' and (workspace / 'portable-command.txt').read_text() == 'command-proof',
                'Explicit host command did not actually execute')
        require(command['evidence']['commands_run'][0]['host_escape_hatch'], 'Host command misreported as sandbox')
        checks['explicit_full_host_command'] = True

        heartbeat = workspace / 'heartbeat.txt'
        code = "import pathlib,time; p=pathlib.Path('heartbeat.txt'); exec('while True:\\n p.write_text(str(time.time()))\\n time.sleep(.1)')"
        arguments.update(args=['-c', 'exec /usr/bin/python3 -c ' + shlex.quote(code)], background=True, check=False)
        background = run('commands', 'start', '--session', chat, '--arguments', json.dumps(arguments), '--json')
        record = json.loads(background['answer'])
        wait_for(heartbeat.exists, 'Background command did not write a heartbeat')
        stopped_record = run('commands', 'stop', '--id', record['id'], '--json')
        require(stopped_record['state'] != 'running', 'Background stop was not recorded')
        if stopped_record['state'] == 'unknown':
            uncertainty.append('Explicit command stop retained UNKNOWN; inspect runtime receipt')
        require(heartbeat.exists(), 'Stop incorrectly undid command side effects')
        old = heartbeat.read_text()
        time.sleep(.35)
        require(heartbeat.read_text() == old, 'Stopped command is still writing')
        checks['background_stop_retains_effects'] = True
        background = run('commands', 'start', '--session', chat, '--arguments', json.dumps(arguments), '--json')
        record = json.loads(background['answer'])
        wait_for(lambda: heartbeat.read_text() != old, 'Second background command did not start')
        stop_owner()
        old = heartbeat.read_text()
        time.sleep(.35)
        require(heartbeat.read_text() == old, 'Owner shutdown left command running')
        checks['owner_shutdown_stops_owned_command'] = True

        start_owner()
        view = api('GET', '/api/chats/' + chat)['view']
        require(view['conversation']['id'] == chat and len(view['turns']) >= 2, 'Restart lost shared conversation')
        retained = api('POST', '/api/attach/inspect', {'run_id': unknown_run})
        require(retained['run']['status'] == 'abandoned' and
                any(attempt['kind'] == 'model' and attempt['status'] == 'unknown' for attempt in retained['attempts']),
                'Ending/restarting discarded the uncertain model attempt')
        shutdown_record = run('commands', 'poll', '--id', record['id'], '--json')
        require(shutdown_record['state'] != 'running', 'Restart reports the old command as still running')
        checks['restart_retains_records'] = True
        checks['uncertainty_survives_restart'] = True
        stop_owner()
        backup, restored = root / 'backup', root / 'restored'
        run('backup', '--out', str(backup))
        run('backup-verify', '--source', str(backup))
        run('restore', '--source', str(backup), '--data-dir', str(restored))
        restored_chats = run('sessions', '--data-dir', str(restored))
        require(any(item['id'] == chat for item in restored_chats), 'Restored store lost conversation identity')
        checks['backup_restore'] = True
        before = hashlib.sha256((data / 'config.json').read_bytes()).hexdigest()
        rejected = run('init', '--workspace', str(workspace), success=False)
        require(rejected.returncode != 0 and hashlib.sha256((data / 'config.json').read_bytes()).hexdigest() == before,
                'Repeated initialization rewrote state')
        bad = root / 'bad-manifest'
        (bad / 'bin').mkdir(parents=True)
        shutil.copy2(cli, bad / 'bin/freeagent')
        (bad / 'manifest.json').write_text('{"data_format":"obsolete"}')
        rejected = subprocess.run([str(bad / 'bin/freeagent'), 'doctor'], env=env, capture_output=True, text=True)
        require(rejected.returncode == 2 and 'manifest' in rejected.stderr and not (bad / 'data').exists(),
                'Bad manifest fell back to another data directory')
        require(not (root / 'fallback-config').exists(), 'Portable commands wrote to per-user configuration')
        checks['refuse_existing_and_invalid_manifest'] = True
        result = {'ok': True, 'candidate': manifest['candidate'], 'target': manifest['target'],
                  'archive_sha256': args.sha256, 'uid': os.getuid(), 'package': str(package),
                  'data_dir': str(data), 'checks': checks, 'remaining_unknown': uncertainty,
                  'real_provider': 'NOT_RUN', 'real_channels': 'NOT_RUN', 'native_desktop': 'NOT_SUPPORTED'}
        print(json.dumps(result, indent=2))
    finally:
        fixture_release.set()
        if fixture:
            fixture.shutdown()
            fixture.server_close()
        if owner:
            try:
                stop_owner()
            except Exception:
                if owner and owner.poll() is None:
                    os.killpg(owner.pid, signal.SIGKILL)
                    owner.wait(timeout=5)
        error_log.close()


if __name__ == '__main__':
    main()
