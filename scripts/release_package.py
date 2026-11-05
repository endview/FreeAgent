#!/usr/bin/env python3
"""Deterministic current-source portable builds; no user state is packaged."""
import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile


ROOT = Path(__file__).resolve().parent.parent


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT).decode().strip()


def build(target, go, output, allow_dirty=False, postgres_runtime=None):
    version = (ROOT / 'VERSION').read_text(encoding='utf-8').strip()
    if not re.fullmatch(r'v\d+\.\d+\.\d+[A-Za-z0-9.+-]*', version):
        raise SystemExit('Invalid VERSION')
    go = shutil.which(go)
    if not go:
        raise SystemExit('Go was not found; specify --go')
    expected = re.search(r'^go (\S+)', (ROOT / 'go.mod').read_text(), re.M)[1]
    go_version = subprocess.check_output([go, 'version'], text=True).strip()
    if f' go{expected} ' not in go_version:
        raise SystemExit(f'Use the pinned Go {expected}; found {go_version}')
    output = Path(output).resolve()
    if output.exists():
        raise SystemExit('Output directory must be new')
    commit = git('rev-parse', 'HEAD')
    epoch = int(git('show', '-s', '--format=%ct', 'HEAD'))
    dirty = bool(git('status', '--porcelain=v1'))
    if dirty and not allow_dirty:
        raise SystemExit('Release builds require a clean checkout; --allow-dirty is for marked development packages only')
    build_version = version + ('+dirty' if dirty else '')
    name = f'FreeAgent-{version}-{target}-portable'
    package = output / 'staging' / name
    (package / 'bin').mkdir(parents=True)
    (package / 'docs').mkdir()
    goos, arch = target.split('-')

    # Runtime files are pinned, separately verified build inputs. A live
    # cluster, connection configuration or user's data directory is never input.
    runtime = Path(postgres_runtime or os.environ.get('FREEAGENT_POSTGRES_RUNTIME', '')).resolve()
    runtime_manifest = runtime / 'runtime-source.json'
    if not runtime_manifest.is_file():
        raise SystemExit('Prepare PG 15.19 with scripts/prepare-postgres.py; pass --postgres-runtime.')
    dependency = json.loads(runtime_manifest.read_text(encoding='utf-8'))
    if dependency.get('version') != '15.19' or dependency.get('target') != target:
        raise SystemExit('PostgreSQL runtime version/target differs from the release contract.')
    for record in dependency['files']:
        relative = Path(record['path'])
        if relative.is_absolute() or '..' in relative.parts or relative.as_posix() != record['path']:
            raise SystemExit('Unsafe PostgreSQL runtime inventory.')
        path = runtime / relative
        if not path.is_file() or path.stat().st_size != record['bytes'] or digest(path) != record['sha256']:
            raise SystemExit('PostgreSQL runtime inventory changed: ' + record['path'])
    suffix = '.exe' if goos == 'windows' else ''
    required = {'bin/' + n + suffix for n in ('postgres','initdb','pg_ctl','pg_dump','pg_restore')}
    library_suffix = '.dll' if goos == 'windows' else '.so'
    required |= {'share/postgres.bki', 'lib/pg_trgm' + library_suffix,
                 'lib/plpgsql' + library_suffix, 'lib/dict_snowball' + library_suffix}
    if not required.issubset({r['path'] for r in dependency['files']}):
        raise SystemExit('PostgreSQL runtime is incomplete.')
    shutil.copytree(runtime, package / 'runtime' / 'postgresql')
    env = dict(os.environ, GOOS=goos, GOARCH=arch, CGO_ENABLED='0', GOTOOLCHAIN='local')
    info = 'github.com/endview/freeagent/internal/buildinfo'
    flags = f'-X {info}.Version={build_version} -X {info}.Commit={commit}'
    def compile(path, source, extra=''):
        subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags',
                        (extra + ' ' + flags).strip(), '-o', str(package / path), source],
                       cwd=ROOT, env=env, check=True)
    if goos == 'windows':
        compile('FreeAgent.exe', './cmd/freeagent-desktop', '-H=windowsgui')
        compile('bin/freeagent.exe', './cmd/freeagent')
        compile('bin/freeagent-channel.exe', './cmd/freeagent-channel')
        readme = 'PORTABLE.md'
    else:
        compile('bin/freeagent', './cmd/freeagent')
        compile('bin/freeagent-channel', './cmd/freeagent-channel')
        launcher = (ROOT / 'scripts/FreeAgent-linux.sh').read_text(encoding='utf-8')
        (package / 'FreeAgent').write_bytes(launcher.replace('\r\n', '\n').encode())
        readme = 'LINUX_PORTABLE.md'
    files = {'docs/' + readme: 'README.md', **{p:p for p in ('VERSION','LICENSE','THIRD_PARTY_NOTICES.md')}}
    files.update({f'docs/{p}': f'docs/{p}' for p in (
        'CONFIGURATION.md','SETTINGS_V7.md','RECOVERY.md','CONTEXT_AND_CONTINUOUS_EXECUTION.md','COMMAND_SESSIONS.md','CHANNELS.md','ATTACHMENTS.md','PUBLIC_WEB.md','POSTGRESQL.md')})
    if goos=='linux':
        files.update({'deploy/freeagent.service':'deploy/freeagent.service',
                      'deploy/freeagent-channel.service':'deploy/freeagent-channel.service',
                      'scripts/install-server.py':'deploy/install-server.py',
                      'deploy/freeagent.Caddyfile':'deploy/freeagent.Caddyfile',
                      'docs/SERVER_DEPLOYMENT.md':'docs/SERVER_DEPLOYMENT.md'})
    for source, destination in files.items():
        (package / destination).parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(ROOT / source, package / destination)
    # git inventory includes embedded dist, workflow and lockfiles, but no
    # ignored outputs, node_modules, credentials, logs or user directories.
    inventory = subprocess.check_output(['git','ls-files','-co','--exclude-standard','-z'],cwd=ROOT)
    source_paths = sorted(set(p.decode() for p in inventory.split(b'\0') if p))
    records = [f'{digest(ROOT / p)}  {p}' for p in source_paths if (ROOT / p).is_file()]
    source_text = '\n'.join(records) + '\n'
    manifest = {
        'candidate':version, 'target': f'{goos}/{arch}', 'cgo_enabled':False,
        'go_version':go_version, 'build_info': f'freeagent version={build_version} commit={commit} target={goos}/{arch}',
        'source_base_commit':commit, 'source_dirty':dirty,
        'source_digest_sha256':hashlib.sha256(source_text.encode()).hexdigest(),
        'source_file_count':len(records), 'archive_reference_utc':datetime.fromtimestamp(epoch,timezone.utc).isoformat(),
        'signed':False,'installer':False,'data_format':'Harness-pg-v1',
        'postgresql':{'version':dependency['version'],'runtime_manifest_sha256':digest(runtime_manifest)},
        'entry':'FreeAgent.exe' if goos=='windows' else 'FreeAgent',
        'native_desktop':goos=='windows', 'interfaces':['cli','loopback_web'],
        'optional_interfaces':['public_web_unix_proxy'] if goos=='linux' else [],
        'bundled_credentials':False,'bundled_user_data':False,
        'files':[{'path':p.relative_to(package).as_posix(),'bytes':p.stat().st_size,'sha256':digest(p)}
                 for p in sorted(package.rglob('*')) if p.is_file()],
    }
    (package / 'manifest.json').write_bytes((json.dumps(manifest,indent=2)+'\n').encode())
    checksums = [f'{digest(p)}  {p.relative_to(package).as_posix()}'
                 for p in sorted(package.rglob('*')) if p.is_file()]
    (package / 'SHA256SUMS.txt').write_bytes(('\n'.join(checksums)+'\n').encode())
    (output / 'SOURCE_SHA256SUMS.txt').write_bytes(source_text.encode())
    if goos=='windows':
        archive=output / (name+'.zip')
        stamp=datetime.fromtimestamp(max(epoch,315532800),timezone.utc).timetuple()[:6]
        with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as zip:
            for path in sorted(package.rglob('*')):
                if path.is_file():
                    item=zipfile.ZipInfo(path.relative_to(output/'staging').as_posix(),stamp)
                    item.create_system=3; item.external_attr=0o100644 << 16
                    zip.writestr(item,path.read_bytes(),compress_type=zipfile.ZIP_DEFLATED,compresslevel=9)
    else:
        archive=output / (name+'.tar.gz')
        with archive.open('wb') as raw, gzip.GzipFile(filename='',mode='wb',fileobj=raw,mtime=epoch,compresslevel=9) as compressed:
            with tarfile.open(fileobj=compressed,mode='w',format=tarfile.USTAR_FORMAT) as tar:
                for path in [package,*sorted(package.rglob('*'))]:
                    relative=path.relative_to(package).as_posix()
                    item=tarfile.TarInfo(name if relative=='.' else name+'/'+relative)
                    item.mtime=epoch;item.uid=item.gid=0;item.uname=item.gname=''
                    executable = relative in ('FreeAgent','bin/freeagent','bin/freeagent-channel') or relative.startswith('runtime/postgresql/bin/')
                    item.mode=0o755 if path.is_dir() or executable else 0o644
                    if path.is_dir():item.type=tarfile.DIRTYPE;tar.addfile(item)
                    else:item.size=path.stat().st_size;tar.addfile(item,io.BytesIO(path.read_bytes()))
    checksum=digest(archive)
    (output / 'SHA256SUMS.txt').write_bytes(f'{checksum}  {archive.name}\n'.encode())
    print(json.dumps({'archive':str(archive),'sha256':checksum,'source_dirty':dirty,'signed':False,'target':target,
        'built_at_utc':datetime.now(timezone.utc).isoformat()},indent=2))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target',required=True,choices=['windows-amd64','linux-amd64'])
    parser.add_argument('--go',default='go'); parser.add_argument('--output-directory',required=True)
    parser.add_argument('--allow-dirty',action='store_true',help='Build a marked development package from an uncommitted checkout')
    parser.add_argument('--postgres-runtime',help='verified prepared PostgreSQL runtime directory')
    args=parser.parse_args()
    build(args.target,args.go,args.output_directory,args.allow_dirty,args.postgres_runtime)


if __name__=='__main__':main()
