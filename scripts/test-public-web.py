#!/usr/bin/env python3
"""Real HTTPS acceptance against an explicitly configured isolated FreeAgent.

No credential is accepted as an argument or written to the result. Optional
--manager exercises local recovery and a loopback protocol fixture; use it only
with the dedicated acceptance instance, never with another user's live state.
"""
import argparse
import base64
import copy
import hashlib
import http.cookiejar
import http.server
import json
import secrets
import ssl
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib
from pathlib import Path


def require(value, message):
    if not value:
        raise RuntimeError(message)


class Client:
    def __init__(self, origin, context):
        self.origin, self.csrf = origin, ''
        self.jar = http.cookiejar.CookieJar()
        self.open = urllib.request.build_opener(urllib.request.HTTPSHandler(context=context),
                                               urllib.request.HTTPCookieProcessor(self.jar))

    def request(self, method, path, value=None, raw=None, headers=None, stream=False):
        payload = raw if raw is not None else json.dumps(value).encode() if value is not None else None
        fields = {'Origin': self.origin, 'User-Agent': 'FreeAgent isolated HTTPS acceptance'}
        if payload is not None:
            fields['Content-Type'] = 'application/octet-stream' if raw is not None else 'application/json'
        if self.csrf:
            fields['X-FreeAgent-CSRF'] = self.csrf
        for key, data in (headers or {}).items():
            if data is None:
                fields.pop(key, None)
            else:
                fields[key] = data
        request = urllib.request.Request(self.origin + path, data=payload, method=method, headers=fields)
        try:
            response = self.open.open(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        if stream:
            return response
        body = response.read(65 << 20)
        status, response_headers = response.status, response.headers
        response.close()
        try:
            result = json.loads(body)
        except (ValueError, UnicodeDecodeError):
            result = None
        return status, result, body, response_headers

    def login(self, password, forged=False):
        status, info, _, _ = self.request('GET', '/api/public/session')
        require(status == 200 and not info['authenticated'], 'login-page discovery failed')
        status, info, _, headers = self.request('POST', '/api/public/login', {'password': password},
            headers={'X-FreeAgent-Client-IP': '203.0.113.79'} if forged else None)
        require(status == 200 and info['authenticated'], 'administrator login failed')
        self.csrf = info['csrf']
        session = next(cookie for cookie in self.jar if cookie.name == '__Host-FreeAgent')
        require(session.secure and session.path == '/' and not session.domain_specified,
                'unsafe session cookie scope')
        cookie_text = '\n'.join(headers.get_all('Set-Cookie')).lower()
        require('httponly' in cookie_text and 'samesite=strict' in cookie_text,
                'missing session cookie restrictions')

    def chat(self, title):
        status, value, _, _ = self.request('POST', '/api/chats', {'title': title})
        require(status == 201, 'conversation creation failed')
        return value['id']

    def view(self, chat):
        status, value, _, _ = self.request('GET', '/api/chats/' + chat)
        require(status == 200, 'conversation read failed')
        return value

    def submit(self, chat, prompt, attachments=None, key=None):
        key = key or secrets.token_hex(16)
        status, _, _, _ = self.request('POST', '/api/chats/' + chat + '/submit',
            {'prompt': prompt, 'request_key': key, 'attachments': attachments or []})
        require(status == 202, 'submission was not accepted')
        return key

    def wait(self, chat, key, expected='completed', seconds=25):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            status, value, _, _ = self.request('GET', f'/api/conversations/{chat}/requests/{key}/status')
            require(status == 200, 'request status unavailable')
            if value['found'] and value['status'] == expected:
                return value['run_id']
            if value.get('status') in ('failed', 'unknown', 'abandoned', 'limited'):
                raise RuntimeError('request ended with ' + value['status'])
            time.sleep(.04)
        raise RuntimeError('request did not reach ' + expected)

    def upload(self, chat, name, body):
        query = urllib.parse.urlencode({'conversation_id': chat, 'name': name,
                                       'request_key': secrets.token_hex(16)})
        status, ref, _, _ = self.request('POST', '/api/attachments?' + query, raw=body)
        require(status == 201, 'attachment upload failed')
        require(ref['sha256'] == hashlib.sha256(body).hexdigest(), 'upload hash mismatch')
        return ref


def png():
    def chunk(kind, body):
        return struct.pack('>I', len(body)) + kind + body + struct.pack('>I', zlib.crc32(kind + body))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 1, 1, 8, 6, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b'\0\xff\0\0\xff')) + chunk(b'IEND', b'')


class ProtocolFixture(http.server.BaseHTTPRequestHandler):
    started = threading.Event()
    finish = threading.Event()
    calls = 0
    filename = ''
    def log_message(self, *args):
        pass

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        type(self).calls += 1
        messages = request['messages']
        index = max(i for i, message in enumerate(messages) if message['role'] == 'user')
        current = messages[index:]
        prompt = current[0].get('content', '')
        tool_results = [m for m in current if m['role'] == 'tool']
        if '[FA_SLOW]' in prompt:
            type(self).started.set()
            type(self).finish.wait(20)
        message = {'role': 'assistant', 'content': 'Protocol fixture completed.'}
        reason = 'stop'
        if '[FA_EXPORT]' in prompt and not any('declined' in m.get('content', '') for m in tool_results):
            if len(tool_results) == 0:
                name, arguments = 'file_create', {'path': self.filename, 'content': 'FA_IMMUTABLE_RESULT 中文\n'}
            elif len(tool_results) == 1:
                name, arguments = 'file_export', {'path': self.filename}
            else:
                name = ''
            if name:
                reason = 'tool_calls'
                message = {'role': 'assistant', 'content': '', 'tool_calls': [
                    {'id': 'call_' + name, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(arguments)}}]}
        result = {'id': 'fixture', 'choices': [{'index': 0, 'message': message, 'finish_reason': reason}],
                  'usage': {'prompt_tokens': 50, 'completion_tokens': 20, 'total_tokens': 70}}
        body = json.dumps(result).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--origin', required=True)
    parser.add_argument('--ca')
    parser.add_argument('--password-stdin', action='store_true')
    parser.add_argument('--manager', help='explicit local CLI wrapper for this isolated Owner')
    parser.add_argument('--unit', help='explicit isolated unit to restart in lifecycle checks')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--idle-expiry', action='store_true', help='exercise actual one-minute expiry and restore policy locally')
    args = parser.parse_args()
    parsed = urllib.parse.urlsplit(args.origin)
    require(parsed.scheme == 'https' and parsed.netloc and not parsed.path, 'exact HTTPS origin required')
    require(not args.idle_expiry or args.manager, 'expiry requires the local restoration manager')
    password = sys.stdin.readline().rstrip('\r\n') if args.password_stdin else __import__('getpass').getpass('Administrator password: ')
    context = ssl.create_default_context(cafile=args.ca)
    result = {'status': 'RUNNING', 'origin': args.origin, 'tls_trust': 'provided_ca' if args.ca else 'system_roots',
              'real_provider': 'NOT_RUN', 'real_channels': 'NOT_RUN', 'checks': {}}
    def record(name):
        result['checks'][name] = True
        args.output.write_text(json.dumps(result, indent=2) + '\n')
    def manager(*values):
        require(args.manager, 'local manager is required for this check')
        completed = subprocess.run([args.manager, *values], input='y\n', text=True, capture_output=True, timeout=25)
        require(completed.returncode == 0, 'local manager operation failed: ' + values[0])
        try:
            return json.loads(completed.stdout)
        except ValueError:
            return {}
    client, stranger = Client(args.origin, context), Client(args.origin, context)
    fixture = None
    original = None
    try:
        for path in ('/api/environment', '/api/events', '/api/chats', '/api/attachments'):
            status, _, _, _ = stranger.request('GET', path, headers={'X-FreeAgent-Key': '0' * 64})
            require(status == 401, 'anonymous or local-capability bypass: ' + path)
        status, _, _, _ = stranger.request('POST', '/api/public/login', {'password': password})
        require(status == 401, 'login accepted without the enabled cookie check')
        client.login(password, forged=True)
        record('authentication_cookie_detection_and_safe_scope')
        status, events, _, _ = client.request('GET', '/api/public/security')
        require(status == 200 and all(e['source'] != '203.0.113.79' for e in events['events']), 'proxy trusted a client IP header')
        for path in ('/api/attach/state', '/api/attach/backup', '/api/remote/state', '/api/public-password', '/api/public-recovery'):
            require(client.request('GET', path)[0] == 404, 'private route exposed')
        require(client.request('GET', '/api/environment', headers={'Origin': 'https://evil.example'})[0] == 403, 'cross-origin read accepted')
        require(client.request('POST', '/api/chats', {'title': 'blocked'}, headers={'X-FreeAgent-CSRF': None})[0] == 403, 'missing CSRF accepted')
        record('exact_origin_csrf_proxy_overwrite_and_private_route_refusal')
        nonce = secrets.token_hex(6)
        chat = client.chat('FA HTTPS ' + nonce)
        require(client.view(chat)['permission']['mode'] == 'none', 'new space/default grant changed')
        key = client.submit(chat, 'FA HTTPS first turn')
        run = client.wait(chat, key)
        client.submit(chat, 'FA HTTPS first turn', key=key)
        require(client.wait(chat, key) == run and len(client.view(chat)['turns']) == 1, 'duplicate replayed')
        if args.manager:
            manager('run', '--session', chat, '--request', 'cli-' + nonce, 'FA CLI continuation')
            require(len(client.view(chat)['turns']) == 2, 'public → CLI → public lost the conversation')
        record('conversation_idempotency_and_shared_cli_continuation')
        text = client.upload(chat, '说明 & !.html', '<script>inert</script> FA_TLS_INPUT 中文'.encode())
        other = Client(args.origin, context)
        other.login(password)
        query = '?conversation_id=' + chat
        require(other.request('GET', '/api/attachments/' + text['id'] + query)[0] == 404, 'another session read staged input')
        key = client.submit(chat, 'Read supplied input', [text['id']])
        run = client.wait(chat, key)
        status, saved, _, _ = client.request('POST', '/api/attachments/reply', {'conversation_id': chat, 'run_id': run, 'request_key': 'save-' + nonce})
        require(status == 201, 'None reply save failed')
        status, _, body, headers = client.request('GET', '/api/attachments/' + saved['id'] + query + '&inline=1')
        require(status == 200 and hashlib.sha256(body).hexdigest() == saved['sha256'] and headers['Content-Type'] == 'application/octet-stream' and headers['Content-Disposition'].startswith('attachment;'), 'reply download not inert/exact')
        picture = client.upload(chat, '红色.png', png())
        status, _, body, headers = client.request('GET', '/api/attachments/' + picture['id'] + query + '&inline=1')
        require(status == 200 and body == png() and headers['Content-Type'] == 'image/png', 'validated image preview failed')
        require(client.request('GET', '/api/attachments/' + saved['id'] + '?conversation_id=other')[0] == 404, 'cross-chat download accepted')
        record('none_text_input_reply_export_image_preview_and_attachment_authorization')
        if args.manager:
            original = client.request('GET', '/api/public-settings')[1]['config']
            lower = copy.deepcopy(original)
            lower['max_permission'] = 'none'
            require(client.request('PUT', '/api/public-settings', lower)[0] == 200, 'remote lowering failed')
            status, info, _, _ = client.request('PUT', '/api/public-settings', original)
            require(status == 409 and info['code'] == 'local_confirmation_required', 'remote raise did not require local confirmation')
            denied = copy.deepcopy(lower)
            denied['security'].update(allowlist_enabled=True, allowlist=['198.51.100.254'])
            status, info, _, _ = client.request('PUT', '/api/public-settings', denied)
            require(status == 409 and info['code'] == 'current_source_blocked', 'remote self-lock not refused')
            manager('control', 'plan', '--kind', 'public_web.set_policy', '--arguments', json.dumps(original))
            record('remote_ceiling_self_lock_and_local_recovery')
            fixture = http.server.ThreadingHTTPServer(('127.0.0.1', 0), ProtocolFixture)
            threading.Thread(target=fixture.serve_forever, daemon=True).start()
            profile, model = 'https-fixture-' + nonce, 'httpsfixture-' + nonce
            manager('key', 'add', '--name', profile, '--url', f'http://127.0.0.1:{fixture.server_port}/v1', '--no-key', '--enable')
            manager('config', 'model', '--id', model, '--key', profile, '--model', 'fixture', '--streaming', 'none')
            tool_chat = client.chat('FA exports ' + nonce)
            require(client.request('POST', '/api/chats/' + tool_chat + '/model', {'model': model})[0] == 200, 'public model switching unavailable')
            require(client.request('POST', '/api/chats/' + tool_chat + '/permission', {'mode': 'full', 'confirmed': True})[0] == 200, 'explicit Full selection failed')
            ProtocolFixture.filename = 'FA-HTTPS-result-' + nonce + '.md'
            key = client.submit(tool_chat, '[FA_EXPORT] create and export controlled fixture')
            client.wait(tool_chat, key)
            refs = [ref for ref in client.view(tool_chat)['turns'][-1]['attachments'] if ref['source'] == 'file_export']
            require(len(refs) == 1, 'file_export did not publish one result')
            ref = refs[0]
            status, _, body, _ = client.request('GET', '/api/attachments/' + ref['id'] + '?conversation_id=' + tool_chat)
            require(status == 200 and body == 'FA_IMMUTABLE_RESULT 中文\n'.encode(), 'exported artifact body differs')
            record('real_owner_file_create_export_and_download_via_protocol_fixture')
            # The same real service continues dispatching after browser logout.
            slow = client.chat('FA retained execution ' + nonce)
            require(client.request('POST', '/api/chats/' + slow + '/model', {'model': model})[0] == 200, 'slow model selection failed')
            key = client.submit(slow, '[FA_SLOW] finish after logout')
            require(ProtocolFixture.started.wait(5), 'real fixture request did not start')
            stream = client.request('GET', '/api/events', stream=True)
            require(stream.status == 200 and b'reconnect' in stream.readline(), 'SSE did not open')
            require(client.request('POST', '/api/public/logout', {})[0] == 200, 'logout failed')
            closed = threading.Event()
            def drain():
                try:
                    stream.read()
                except (OSError, ValueError):
                    pass
                finally:
                    stream.close()
                    closed.set()
            threading.Thread(target=drain, daemon=True).start()
            require(closed.wait(3), 'logout left SSE open')
            ProtocolFixture.finish.set()
            other.wait(slow, key)
            require(client.request('GET', '/api/environment')[0] == 401, 'logged-out credential retained access')
            client = other
            record('logout_closes_real_sse_and_preserves_an_inflight_task')
            # Authenticated clients remain usable when source password failures lock login.
            bad = Client(args.origin, context)
            for _ in range(original['security']['failure_limit']):
                require(bad.request('GET', '/api/public/session')[0] == 200, 'login source locked too early')
                require(bad.request('POST', '/api/public/login', {'password': 'wrong fixture password'})[0] == 401, 'wrong password accepted')
            require(bad.request('GET', '/api/public/session')[0] == 403, 'failure lock did not block login source')
            require(client.request('GET', '/api/environment')[0] == 200, 'login lock invalidated an existing session')
            manager('public', 'clear-locks')
            record('actual_password_failure_lock_existing_session_and_local_unlock')
            if args.idle_expiry:
                idle = copy.deepcopy(original)
                idle['idle_minutes'] = 1
                require(client.request('PUT', '/api/public-settings', idle)[0] == 200, 'idle policy not accepted')
                timer_client = Client(args.origin, context)
                timer_client.login(password)
                started = time.monotonic()
                while time.monotonic() - started < 62:
                    time.sleep(min(1, 62 - (time.monotonic() - started)))
                require(timer_client.request('GET', '/api/environment')[0] == 401, 'actual idle expiry not enforced')
                manager('control', 'plan', '--kind', 'public_web.set_policy', '--arguments', json.dumps(original))
                client = Client(args.origin, context)
                client.login(password)
                result['actual_idle_seconds'] = round(time.monotonic() - started, 3)
                record('actual_one_minute_idle_expiry')
            if args.unit:
                completed = subprocess.run(['systemctl', 'restart', args.unit], capture_output=True, timeout=25)
                require(completed.returncode == 0, 'isolated Owner restart failed')
                time.sleep(.5)
                require(client.request('GET', '/api/environment')[0] == 401, 'Owner restart retained session')
                client = Client(args.origin, context)
                client.login(password)
                record('real_owner_restart_requires_reauthentication')
        result['status'] = 'PASS'
        result['visual_provider_semantics'] = 'NOT_RUN'
        args.output.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
    except BaseException as error:
        result['status'] = 'FAIL'
        result['failure'] = type(error).__name__ + ': ' + str(error)
        args.output.write_text(json.dumps(result, indent=2) + '\n')
        raise
    finally:
        ProtocolFixture.finish.set()
        if fixture:
            fixture.shutdown()
        if original and args.manager:
            manager('control', 'plan', '--kind', 'public_web.set_policy', '--arguments', json.dumps(original))


if __name__ == '__main__':
    main()
