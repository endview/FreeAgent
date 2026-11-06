#!/usr/bin/env python3
"""Run browser acceptance against a new Core and isolated temporary PostgreSQL.

Build the CLI first, install ChatUI's pinned Playwright browsers, then pass
--engine and --runtime. This script never attaches to an existing Owner.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--engine', required=True)
    parser.add_argument('--runtime', required=True)
    parser.add_argument('--output', default='output/frontend-acceptance')
    parser.add_argument('--channel', default='')
    parser.add_argument('--under-pg', action='store_true', help=argparse.SUPPRESS)
    parser.add_argument('--development-assets', default='')
    parser.add_argument('--benchmark', action='store_true')
    parser.add_argument('--api-only', action='store_true')
    parser.add_argument('--go', default='go')
    parser.add_argument('--only', default='', help='Comma-separated browser groups for a targeted rerun')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[1]
    engine = Path(args.engine).resolve()
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    if not args.under_pg:
        command = [sys.executable, str(repo / 'scripts/test-postgres.py'), '--runtime',
                   args.runtime, '--', sys.executable, str(Path(__file__).resolve()),
                   *sys.argv[1:], '--under-pg']
        return subprocess.run(command, cwd=repo).returncode
    root = Path(tempfile.mkdtemp(prefix='core-', dir=output))
    work, data = root / 'workspace', root / 'data'
    work.mkdir()
    env = dict(os.environ, FREEAGENT_KEYS_FILE=str(root / 'keys.json'),
               FREEAGENT_UI_TEST_ROOT=str(root), FREEAGENT_UI_ARTIFACTS=str(root / 'browser'),
               FREEAGENT_BROWSER_CHANNEL=args.channel, FREEAGENT_UI_API_ONLY='1' if args.api_only else '')
    if args.development_assets:
        env['FREEAGENT_UI_DEV_ROOT'] = str(Path(args.development_assets).resolve())
        env['FREEAGENT_UI_GALLERY'] = '1'
    with (root / 'setup.log').open('wb') as log:
        for command in [['init', '--data-dir', str(data), '--workspace', str(work)],
                        ['config', 'model', '--data-dir', str(data), '--id', 'echo', '--provider', 'echo']]:
            subprocess.run([str(engine), *command], env=env, stdout=log, stderr=log, check=True, timeout=60)
    if args.benchmark:
        subprocess.run([args.go, 'run', str(repo / 'scripts/seed-ui-benchmark.go')],
                       cwd=repo, env=env, check=True, timeout=180)
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    with (root / 'owner.log').open('wb') as log:
        process = subprocess.Popen([str(engine), 'web', '--data-dir', str(data), '--port', str(port)],
                                   env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 45
            while not (data / 'engine.json').is_file():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('Isolated Core did not become ready; see ' + str(root / 'owner.log'))
                time.sleep(0.1)
            groups=['core', 'provider', 'tool-core', 'settings', 'continuity', 'bridge', 'browsers']
            names=['performance'] if args.benchmark else args.only.split(',') if args.only else groups
            if any(name not in [*groups,'performance','affectweave'] for name in names):
                raise ValueError('Unknown browser test group')
            for name in names:
                result = subprocess.run(['node', str(repo / f'internal/chatui/scripts/browser-{name}.cjs')],
                                        cwd=repo, env=env, timeout=900 if args.benchmark else 240)
                if result.returncode:
                    return result.returncode
            print('Browser acceptance passed; artifacts: ' + str(root / 'browser'))
            return 0
        finally:
            if process.poll() is None:
                try:
                    info = json.loads((data / 'engine.json').read_text(encoding='utf-8'))
                    key = (data / 'chat.secret').read_text(encoding='utf-8').strip()
                    request = urllib.request.Request(info['origin'] + '/api/attach/handoff',
                        data=b'{"cancel_active":true}', headers={'Origin': info['origin'],
                        'X-FreeAgent-Key': key, 'Content-Type': 'application/json'})
                    urllib.request.urlopen(request, timeout=5).close()
                    process.wait(timeout=20)
                except Exception:
                    process.terminate()
                    process.wait(timeout=15)


if __name__ == '__main__':
    raise SystemExit(main())
