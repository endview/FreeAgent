#!/usr/bin/env python3
"""Prepare the pinned PG 15 runtime. Build inputs are private-cache artifacts,
never a running database. Linux builds must use Debian 12 (including a container).
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import tarfile
import urllib.request
import zipfile

VERSION = '15.19'
SOURCES = {
    'source': ('https://ftp.postgresql.org/pub/source/v15.19/postgresql-15.19.tar.bz2',
               'e1a64a87a46b825b88c082e4518161a47aab53c45694964f8ba1df28f7859f89'),
    'windows': ('https://get.enterprisedb.com/postgresql/postgresql-15.19-1-windows-x64-binaries.zip',
                'cc54e2d349f48c7743ad0c509b755ea565ff04bb5c5e71745d114074cab0c453'),
    'vc': ('https://aka.ms/vs/17/release/vc_redist.x64.exe',
           'cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b'),
}
CONFIGURE = ['--prefix=/opt/freeagent-postgresql', '--without-readline', '--without-icu',
             '--without-ldap', '--without-gssapi', '--without-pam', '--without-systemd',
             '--without-openssl', '--without-zstd', '--without-lz4', '--disable-rpath']
PROGRAMS = ['postgres', 'pg_ctl', 'initdb', 'pg_dump', 'pg_restore', 'pg_isready', 'psql']

def native_licenses(cache, out):
    references = json.loads(Path(__file__).with_name('postgres-native-licenses.json').read_text(encoding='utf-8'))
    cached = cache / 'native-licenses'
    cached.mkdir(exist_ok=True)
    (out / 'licenses').mkdir(exist_ok=True)
    for name, url in references.items():
        path = cached / name
        if not path.exists():
            with urllib.request.urlopen(url, timeout=45) as response:
                path.write_bytes(response.read())
        shutil.copyfile(path, out / 'licenses' / name)
    (out / 'licenses' / 'SOURCES.json').write_text(json.dumps(references, indent=2) + '\n', encoding='utf-8')
    (out / 'licenses' / 'Native-dependencies.txt').write_text('''Unmodified native dependencies in the pinned EDB Windows distribution:
ICU 67.1; OpenSSL 3.5.7; libiconv 1.15; GNU gettext/libintl 0.19.8;
LZ4 1.10.0; libxml2 2.15.3; Zstandard 1.5.7; zlib 1.3.2; MinGW-w64 Winpthreads.
Microsoft VC runtime 14.44.35211.0 is extracted from the pinned Microsoft redistributable.

Library license texts and copyright notices accompany these files. The MinGW and
GNU components remain separate replaceable DLLs; FreeAgent imposes no additional
restriction on modifying/relinking them or debugging those modifications.
Corresponding upstream source releases:
https://ftp.gnu.org/pub/gnu/libiconv/libiconv-1.15.tar.gz
https://ftp.gnu.org/pub/gnu/gettext/gettext-0.19.8.tar.gz
https://github.com/mingw-w64/mingw-w64/tree/v12.0.0/mingw-w64-libraries/winpthreads
Other upstream source and license references are in SOURCES.json. PostgreSQL's
binary/source inputs are in runtime-source.json. No native source modifications
are made by FreeAgent.
''', encoding='utf-8')

def digest(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()

def download(kind, cache):
    url, expected = SOURCES[kind]
    path = cache / ('vc_redist.x64.exe' if kind == 'vc' else url.rsplit('/', 1)[1])
    if not path.exists():
        temporary = path.with_suffix(path.suffix + '.part')
        with urllib.request.urlopen(url, timeout=90) as response, temporary.open('wb') as out:
            shutil.copyfileobj(response, out)
        temporary.replace(path)
    if digest(path) != expected:
        raise SystemExit('Pinned runtime download differs: ' + path.name)
    return path

def pe_imports(path):
    b = path.read_bytes()
    pe = struct.unpack_from('<I', b, 60)[0]
    n = struct.unpack_from('<H', b, pe + 6)[0]
    size = struct.unpack_from('<H', b, pe + 20)[0]
    opt, table = pe + 24, pe + 24 + size
    sections = [struct.unpack_from('<IIII', b, table + 40 * i + 8) for i in range(n)]
    def offset(rva):
        for vs, va, rs, fo in sections:
            if va <= rva < va + max(vs, rs):
                return fo + rva - va
        return rva
    rva = struct.unpack_from('<I', b, opt + (120 if struct.unpack_from('<H', b, opt)[0] == 0x20b else 104))[0]
    if not rva:
        return []
    pos, names = offset(rva), []
    while any(b[pos:pos + 20]):
        name = offset(struct.unpack_from('<I', b, pos + 12)[0])
        names.append(b[name:b.index(0, name)].decode())
        pos += 20
    return names

def expand_cab(path, target):
    target.mkdir(parents=True, exist_ok=True)
    subprocess.run(['expand.exe', '-F:*', str(path), str(target)], check=True, stdout=subprocess.DEVNULL)

def windows(cache, out):
    if sys.platform != 'win32':
        raise SystemExit('Prepare the Windows runtime on Windows.')
    # Re-extract verified source bytes; stale writable unpacked caches are not
    # trusted as the binary input to a later release.
    extracted = Path(tempfile.mkdtemp(prefix='pg-prepare-', dir=cache)).resolve()
    with zipfile.ZipFile(download('windows', cache)) as z:
        for member in z.infolist():
            if member.filename.startswith(('pgsql/bin/', 'pgsql/lib/', 'pgsql/share/')) or member.filename == 'pgsql/doc/postgresql/html/legalnotice.html':
                z.extract(member, extracted)
    source = extracted / 'pgsql'
    redist = download('vc', cache)
    # Extract cabinets; do not install a system redistributable or require an
    # interactive Windows installer in the product's startup flow.
    raw, pos, number = redist.read_bytes(), 0, 0
    vc = extracted / 'vc'
    vc.mkdir(exist_ok=True)
    while True:
        pos = raw.find(b'MSCF', pos)
        if pos < 0:
            break
        size = struct.unpack_from('<I', raw, pos + 8)[0]
        if 36 <= size <= len(raw) - pos and raw[pos + 24:pos + 26] == b'\x03\x01':
            cabinet = vc / f'payload-{number}.cab'
            cabinet.write_bytes(raw[pos:pos + size])
            expanded = vc / f'payload-{number}'
            expand_cab(cabinet, expanded)
            for inner in expanded.iterdir():
                if inner.is_file() and inner.read_bytes()[:4] == b'MSCF':
                    expand_cab(inner, vc / 'dlls')
                elif inner.is_file() and inner.read_bytes().startswith(b'{\\rtf'):
                    (out / 'licenses' / 'Microsoft').mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(inner, out / 'licenses' / 'Microsoft' / (inner.name + '.rtf'))
            number += 1
        pos += 4
    for name in ['msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll']:
        shutil.copyfile(vc / 'dlls' / (name + '_amd64'), source / 'bin' / name)
    for name in ['bin', 'lib']:
        (out / name).mkdir(exist_ok=True)
    queue = [source / 'bin' / (n + '.exe') for n in PROGRAMS]
    # initdb installs the built-in text-search dictionaries even when the
    # product uses only pg_trgm for explicit keyword search.
    queue += [source / 'lib' / n for n in ['plpgsql.dll', 'dict_snowball.dll', 'pg_trgm.dll']]
    seen = set()
    while queue:
        path = queue.pop()
        if path.name.lower() in seen:
            continue
        seen.add(path.name.lower())
        shutil.copyfile(path, out / ('lib' if path.parent == source / 'lib' else 'bin') / path.name)
        for name in pe_imports(path):
            dependency = next((d / name for d in [source / 'bin', source / 'lib'] if (d / name).exists()), None)
            if dependency:
                queue.append(dependency)
    shutil.copytree(source / 'share', out / 'share')
    for path in (out / 'share' / 'extension').iterdir():
        if not path.name.startswith(('plpgsql', 'pg_trgm')):
            path.unlink()
    shutil.copyfile(source / 'doc' / 'postgresql' / 'html' / 'legalnotice.html', out / 'licenses' / 'PostgreSQL.html')
    if extracted.parent != cache or not extracted.name.startswith('pg-prepare-'):
        raise RuntimeError('Refusing unexpected preparation cleanup path')
    shutil.rmtree(extracted)

def linux(cache, out):
    release = Path('/etc/os-release').read_text()
    if 'ID=debian' not in release or 'VERSION_ID="12"' not in release:
        raise SystemExit('Build the Linux PG runtime on Debian 12; newer glibc is not a Debian 12 portable runtime.')
    source = cache / ('postgresql-' + VERSION)
    if not source.exists():
        with tarfile.open(download('source', cache)) as tar:
            # Debian 12's Python 3.11 predates extraction filters. The pinned
            # source archive needs only directories and regular files.
            for member in tar.getmembers():
                target = (cache / member.name).resolve()
                if not target.is_relative_to(cache.resolve()) or not (member.isdir() or member.isfile()):
                    raise RuntimeError('Unexpected PostgreSQL source archive entry')
            tar.extractall(cache)
    subprocess.run(['./configure', *CONFIGURE], cwd=source, check=True)
    subprocess.run(['make', '-j2'], cwd=source, check=True)
    subprocess.run(['make', '-C', 'contrib/pg_trgm', '-j2'], cwd=source, check=True)
    stage = cache / 'stage'
    subprocess.run(['make', 'DESTDIR=' + str(stage), 'install'], cwd=source, check=True)
    subprocess.run(['make', '-C', 'contrib/pg_trgm', 'DESTDIR=' + str(stage), 'install'], cwd=source, check=True)
    shutil.copytree(stage / 'opt/freeagent-postgresql', out, dirs_exist_ok=True)
    for path in out.rglob('*.a'):
        path.unlink()
    shutil.rmtree(out / 'include')
    shutil.rmtree(out / 'lib/pkgconfig')
    shutil.copyfile(source / 'COPYRIGHT', out / 'LICENSE')

def stamp(out, target):
    records = [{'path': p.relative_to(out).as_posix(), 'bytes': p.stat().st_size, 'sha256': digest(p)}
               for p in sorted(out.rglob('*')) if p.is_file() and p.name != 'runtime-source.json']
    used_sources = {k: v for k, v in SOURCES.items() if target == 'windows-amd64' or k == 'source'}
    metadata = {'version': VERSION, 'target': target, 'sources': used_sources, 'files': records}
    if target == 'linux-amd64':
        metadata.update(configure=CONFIGURE, compiler=subprocess.check_output(['gcc', '--version'], text=True).splitlines()[0])
    (out / 'runtime-source.json').write_text(json.dumps(metadata, indent=2) + '\n', encoding='utf-8')

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--target', choices=['windows-amd64', 'linux-amd64'], required=True)
    p.add_argument('--cache', required=True)
    p.add_argument('--out', required=True, help='new runtime directory (named postgresql in release layout)')
    args = p.parse_args()
    cache, out = Path(args.cache).resolve(), Path(args.out).resolve()
    if out.exists():
        raise SystemExit('Runtime output must be new.')
    cache.mkdir(parents=True, exist_ok=True)
    out.mkdir(parents=True)
    (windows if args.target == 'windows-amd64' else linux)(cache, out)
    if args.target == 'windows-amd64':
        native_licenses(cache, out)
    stamp(out, args.target)
    print(json.dumps({'runtime': str(out), 'version': VERSION, 'target': args.target}))

if __name__ == '__main__':
    main()
