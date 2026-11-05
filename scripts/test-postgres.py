#!/usr/bin/env python3
"""Run real-PG tests with one isolated temporary service, then stop it.
Usage: python scripts/test-postgres.py --runtime <postgresql> -- go test ...
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import socket
import subprocess
import tempfile

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runtime', required=True)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('provide the test command after --')
    if os.name != 'nt' and os.geteuid() == 0:
        raise SystemExit('Run PG acceptance as a non-root user.')
    runtime = Path(args.runtime).resolve()
    bin = runtime / 'bin'
    ctl = bin / ('pg_ctl.exe' if os.name == 'nt' else 'pg_ctl')
    root = Path(tempfile.mkdtemp(prefix='freeagent-pg-tests-')).resolve()
    cluster, password = root / 'cluster', secrets.token_urlsafe(32)
    pw = root / 'password'
    pw.write_text(password, encoding='utf-8')
    pw.chmod(0o600)
    env = dict(os.environ)
    if os.name != 'nt':
        env['LD_LIBRARY_PATH'] = str(runtime / 'lib')
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    locale = 'en-US.utf8' if os.name == 'nt' else 'C.UTF-8'
    quote = (lambda s: '"' + str(s).replace('"', '\\"') + '"') if os.name == 'nt' else lambda s: shlex.quote(str(s))
    started = False
    try:
        # Regular files avoid pg_ctl descendant pipe inheritance on Windows.
        with (root / 'setup.log').open('wb') as log:
            subprocess.run([str(ctl), 'initdb', '-D', str(cluster), '-o',
                            f'-U fa_admin --pwfile={quote(pw)} --auth=scram-sha-256 --encoding=UTF8 --locale={locale}'],
                           env=env, stdout=log, stderr=log, check=True, timeout=60)
            pw.unlink()
            with (cluster / 'postgresql.conf').open('a') as f:
                f.write("\nlog_min_error_statement = 'panic'\n")
            subprocess.run([str(ctl), 'start', '-D', str(cluster), '-l', str(root / 'postgres.log'), '-w', '-t', '30',
                            '-o', f'-h 127.0.0.1 -p {port} -c max_connections=64 -c shared_buffers=64MB'],
                           env=env, stdout=log, stderr=log, check=True, timeout=45)
        started = True
        service = root / 'service.json'
        service.write_text(json.dumps({'host': '127.0.0.1', 'port': port, 'user': 'fa_admin',
                                      'password': password, 'database': 'postgres', 'bin': str(bin)}), encoding='utf-8')
        service.chmod(0o600)
        env['FREEAGENT_PG_SERVICE_FILE'] = str(service)
        env['FREEAGENT_POSTGRES_BIN'] = str(bin)
        result = subprocess.run(command, env=env)
        return result.returncode
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        print('Temporary PostgreSQL acceptance service failed; setup diagnostics retained at ' + str(root))
        raise
    finally:
        if started:
            with (root / 'stop.log').open('wb') as log:
                # The full suite creates hundreds of independent databases;
                # its shutdown checkpoint is much larger than a normal Core's.
                stopped = subprocess.run([str(ctl), 'stop', '-D', str(cluster), '-m', 'fast', '-w', '-t', '180'],
                                         env=env, stdout=log, stderr=log, timeout=195)
            if stopped.returncode == 0:
                # The only recursive delete is the exact directory created by
                # mkdtemp above; runtime and project directories are excluded.
                if root.parent != Path(tempfile.gettempdir()).resolve() or not root.name.startswith('freeagent-pg-tests-'):
                    raise RuntimeError('Refusing unexpected cleanup path')
                shutil.rmtree(root)
            else:
                print('Temporary PG did not stop; retained its directory at ' + str(root))

if __name__ == '__main__':
    raise SystemExit(main())
