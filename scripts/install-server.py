#!/usr/bin/env python3
"""Explicit Debian/systemd installation. Never removes user data or overwrites unknown units."""
import argparse
import hashlib
import json
import os
from pathlib import Path
try:
    import pwd
    import grp
except ImportError:  # --help and package-verification tests also work on Windows.
    pwd = grp = None
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import uuid

INSTALL_ROOT = Path('/opt')
MANAGER_ROOT = Path('/usr/local/bin')
UNIT_ROOT = Path('/etc/systemd/system')
STATE_ROOT = Path('/var/lib')
RUNTIME_ROOT = Path('/run')


def refuse(message, action):
    raise SystemExit(message + '\nNext: ' + action)


def installation_path(value, root):
    path = Path(value).absolute()
    if '..' in path.parts or not path.is_relative_to(root) or path == root:
        refuse('Invalid installation path: ' + str(path), 'Choose an absolute path below ' + str(root) + ' without .. components.')
    for part in (path, *path.parents):
        if part.is_symlink():
            refuse('Installation path contains a symbolic link: ' + str(part), 'Choose a real directory/path; no installation files were changed.')
    systemd(path)
    return path


def verify_package(package):
    if (package / 'data').exists() or (package / 'workspace').exists():
        refuse('Package already contains user data.', 'Extract a clean release into a new directory; keep the current data where it is.')
    manifest_path = package / 'manifest.json'
    if manifest_path.is_symlink() or not manifest_path.is_file() or manifest_path.stat().st_size > 1 << 20:
        refuse('Unsafe or oversized package manifest.', 'Extract a fresh portable release into a new directory.')
    manifest = json.loads(manifest_path.read_text())
    if (manifest['target'] != 'linux/amd64' or manifest['source_dirty'] is not False
            or not re.fullmatch(r'v[0-9][A-Za-z0-9.+-]*', manifest['candidate'])
            or not re.fullmatch(r'[0-9a-f]{40}', manifest['source_base_commit'])
            or not re.fullmatch(r'Harness-v[0-9]+', manifest['data_format'])):
        refuse('Package is not a clean Linux amd64 release.', 'Download the Linux portable release and verify its published SHA-256.')
    if not isinstance(manifest['files'], list) or not 1 <= len(manifest['files']) <= 2048:
        refuse('Invalid package file inventory.', 'Extract a fresh portable release.')
    hashes = {}
    for record in manifest['files']:
        name = record['path']
        if (not isinstance(name, str) or '\\' in name or any(ord(c) < 32 for c in name)
                or Path(name).is_absolute() or '..' in Path(name).parts or Path(name).as_posix() != name
                or name in hashes or name in ('manifest.json', 'SHA256SUMS.txt')):
            refuse('Unsafe or duplicate payload path.', 'Extract a fresh portable release.')
        path = package / name
        if not path.resolve().is_relative_to(package) or not path.is_file() or path.is_symlink():
            refuse('Missing or unsafe package file: ' + name, 'Extract the complete portable release without changing its files.')
        if not re.fullmatch(r'[0-9a-f]{64}', record['sha256']) or path.stat().st_size != record['bytes']:
            refuse('Package size or digest metadata differs: ' + name, 'Download and verify the release again.')
        with path.open('rb') as source:
            hashes[name] = hashlib.file_digest(source, 'sha256').hexdigest()
        if hashes[name] != record['sha256']:
            refuse('Package integrity failure: ' + name, 'Download and verify the release again.')
    if not {'FreeAgent', 'bin/freeagent'}.issubset(hashes):
        refuse('Package does not declare its required executables.', 'Use the complete Linux portable release.')
    expected = set(hashes) | {'manifest.json', 'SHA256SUMS.txt'}
    actual = set()
    for count, path in enumerate(package.rglob('*'), 1):
        if count > 8192:
            refuse('Package inventory exceeds the portable release limit.', 'Use a clean extracted portable release.')
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            refuse('Package contains a link or special file.', 'Use a clean extracted portable release.')
        if path.is_file():
            actual.add(path.relative_to(package).as_posix())
    if actual != expected:
        refuse('Package contains missing or additional files/user data.', 'Extract a clean release into a new directory; keep existing user data in its current location.')
    hashes['manifest.json'] = hashlib.sha256(manifest_path.read_bytes()).hexdigest()
    checksums = package / 'SHA256SUMS.txt'
    if checksums.stat().st_size > 1 << 20:
        refuse('Oversized package checksum list.', 'Extract a fresh portable release.')
    declared = {}
    for line in checksums.read_text().splitlines():
        digest, name = line.split('  ', 1)
        if name in declared:
            refuse('Duplicate package checksum entry.', 'Extract a fresh portable release.')
        declared[name] = digest
    if declared != hashes:
        refuse('Package checksum list differs from its payload.', 'Download and verify the release again.')
    return manifest


def preflight(args):
    if sys.platform != 'linux' or pwd is None:
        refuse('The server installer requires Linux with systemd.', 'Use the Windows portable launcher on Windows.')
    if os.geteuid() != 0:
        refuse('Run the installer or --check as root; the installed service runs as its dedicated user.', 'Rerun this command with sudo.')
    if os.uname().machine not in ('x86_64', 'amd64'):
        refuse('This release requires Linux amd64.', 'Use a supported amd64 host.')
    for value in (args.user, args.unit):
        if not re.fullmatch(r'[a-z_][a-z0-9_-]{1,30}', value):
            refuse('Invalid user/unit name.', 'Use 2-31 lowercase letters, digits, underscores or hyphens, starting with a letter or underscore.')
    if not 1 <= args.port <= 65535:
        refuse('Invalid loopback port.', 'Choose a port from 1 to 65535.')
    required = ['systemctl', 'systemd-analyze', 'runuser', 'useradd']
    if args.public_proxy_user:
        required += ['groupadd', 'usermod']
    missing = [name for name in required if shutil.which(name) is None]
    if missing or not (RUNTIME_ROOT / 'systemd/system').is_dir():
        refuse('Missing systemd/service prerequisites: ' + (', '.join(missing) or 'systemd runtime'), 'Use Debian with running systemd and install systemd, util-linux and passwd; otherwise use portable mode.')
    package = Path(args.package).resolve()
    prefix, manager = installation_path(args.prefix, INSTALL_ROOT), installation_path(args.manager, MANAGER_ROOT)
    if not manager.parent.is_dir():
        refuse('Management-command parent directory does not exist.', 'Create/review ' + str(manager.parent) + ' before installing.')
    manifest = verify_package(package)
    unit = UNIT_ROOT / (args.unit + '.service')
    receipt = prefix / '.freeagent-install.json'
    if unit.is_symlink() or receipt.is_symlink() or (receipt.exists() and (not receipt.is_file() or receipt.stat().st_size > 1 << 20)):
        refuse('Unit or install receipt is unsafe or oversized.', 'Review the existing installation; no files were changed.')
    owned = json.loads(receipt.read_text()) if receipt.exists() else None
    if owned is not None and not isinstance(owned, dict):
        refuse('Invalid installation receipt.', 'Review the saved receipt before updating; no files were changed.')
    if (unit.exists() or manager.exists() or prefix.exists()) and not owned:
        refuse('Unknown installation paths already exist.', 'Choose unused --prefix, --unit and --manager paths; existing files were not overwritten.')
    identity = {'user': args.user, 'unit': args.unit, 'prefix': str(prefix), 'manager': str(manager)}
    if owned and any(owned.get(k) != v for k, v in identity.items()):
        refuse('Existing installation identity differs.', 'Repeat the original user, unit, prefix and manager options.')
    if owned and (not unit.is_file() or unit.read_text() != owned['unit_text']):
        refuse('Unit is missing or has local edits.', 'Review the unit and the saved installation receipt before updating.')
    if owned and (not manager.is_file() or manager.read_text() != owned.get('manager_text')):
        refuse('Management command is missing or has local edits.', 'Review the manager and the saved installation receipt before updating.')
    if owned and subprocess.run(['systemctl', 'is-active', '--quiet', args.unit]).returncode == 0:
        refuse('Installed service is active.', 'Create and verify an online backup, then run systemctl stop ' + args.unit + ' and repeat --check before updating.')
    with socket.socket() as probe:
        if os.name == 'posix':
            # Match the Go listener: stopped connections in TIME_WAIT do not
            # make a port occupied. A live TCP listener still prevents binding.
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind(('127.0.0.1', args.port))
        except OSError:
            refuse('Loopback port is occupied.', 'Choose a free --port or stop the process using it; no service was changed.')
    try:
        account = pwd.getpwnam(args.user)
    except KeyError:
        account = None
    if account and (account.pw_uid == 0 or account.pw_gecos != 'FreeAgent service'):
        refuse('Refusing to reuse an unrelated account.', 'Choose an unused dedicated --user name.')
    state = STATE_ROOT / args.unit
    for path in (state, state / 'workspace', state / 'data'):
        if path.is_symlink() or (path.exists() and (not path.is_dir() or account is None or path.stat().st_uid != account.pw_uid)):
            refuse('State path belongs to another user or is not a real directory.', 'Review ' + str(path) + '; use a separate --unit for a new installation. No account or state directory was changed.')
    if args.public_proxy_user:
        if not re.fullmatch(r'[a-z_][a-z0-9_-]{1,30}', args.public_group):
            refuse('Invalid dedicated public group.', 'Use a valid dedicated group name.')
        try:
            proxy = pwd.getpwnam(args.public_proxy_user)
        except KeyError:
            refuse('Public proxy account does not exist.', 'Install the TLS proxy first, then pass its existing non-root account with --public-proxy-user.')
        if proxy.pw_uid == 0 or args.public_proxy_user == args.user or (account and proxy.pw_uid == account.pw_uid):
            refuse('Proxy must use a separate non-root identity.', 'Choose the existing dedicated proxy account, distinct from the FreeAgent service user.')
    elif owned and '\nSupplementaryGroups=' in owned['unit_text']:
        refuse('The existing installation includes public proxy access.', 'Repeat its --public-proxy-user and --public-group options when updating.')
    current = prefix / 'current'
    if current.exists() and not current.is_symlink():
        refuse('Unknown current program path.', 'Review ' + str(current) + ' before updating; it was not replaced.')
    if current.is_symlink() and not current.resolve().is_relative_to(prefix / 'releases'):
        refuse('Current program points outside this installation.', 'Review the program link before updating; it was not replaced.')
    target = installation_path(str(prefix / 'releases' / (manifest['candidate'] + '-' + manifest['source_base_commit'][:12])), INSTALL_ROOT)
    if target.exists():
        if target.is_symlink() or verify_package(target) != manifest:
            refuse('Existing release differs.', 'Keep the existing release and choose a new verified release; nothing was overwritten.')
    warnings = []
    if shutil.which('bwrap') is None:
        warnings.append('Optional Linux sandbox dependency bubblewrap is absent. Full/host can still use the service user permissions. Install bubblewrap only if sandbox execution is needed.')
    return {'package': package, 'prefix': prefix, 'manager': manager, 'manifest': manifest,
            'unit': unit, 'receipt': receipt, 'owned': owned, 'identity': identity,
            'account': account, 'target': target, 'state': state, 'warnings': warnings}


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def atomic_text(path, text, mode):
    descriptor, temporary = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    temporary = Path(temporary)
    try:
        with os.fdopen(descriptor, 'w', encoding='utf-8', newline='\n') as output:
            output.write(text)
            output.flush()
            os.fsync(output.fileno())
        temporary.chmod(mode)
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def systemd(value):
    if any(ord(c)<32 for c in str(value)):
        raise SystemExit('Control characters are not allowed in installation paths')
    return '"'+str(value).replace('\\','\\\\').replace('"','\\"').replace('%','%%')+'"'


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--package', required=True)
    parser.add_argument('--prefix', default='/opt/freeagent')
    parser.add_argument('--user', default='freeagent')
    parser.add_argument('--unit', default='freeagent')
    parser.add_argument('--port', type=int, default=8765)
    parser.add_argument('--manager', default='/usr/local/bin/freeagent')
    parser.add_argument('--start', action='store_true')
    parser.add_argument('--check', action='store_true', help='validate package, dependencies, paths, accounts and port without installing or changing the service')
    parser.add_argument('--public-proxy-user', help='explicitly prepare a restricted runtime directory for this existing TLS proxy user; does not enable public Web')
    parser.add_argument('--public-group', default='freeagent-public')
    args=parser.parse_args()
    checked = preflight(args)
    package, prefix, manager = (checked[k] for k in ('package', 'prefix', 'manager'))
    manifest, unit, receipt, owned = (checked[k] for k in ('manifest', 'unit', 'receipt', 'owned'))
    identity, account, target, state = (checked[k] for k in ('identity', 'account', 'target', 'state'))
    data_format=manifest['data_format']
    if args.check:
        print(json.dumps({'ok':True,'check_only':True,'changes_made':False,'unit':args.unit,
                          'prefix':str(prefix),'manager':str(manager),'data_dir':str(state/'data'/data_format),
                          'warnings':checked['warnings'],'next':'Rerun the same command without --check to install. Add --start to start the service.'}))
        return
    if account is None:
        run('useradd','--system','--user-group','--home-dir',str(state),'--shell','/usr/sbin/nologin','--comment','FreeAgent service',args.user)
        account=pwd.getpwnam(args.user)
    if account.pw_uid==0 or account.pw_gecos!='FreeAgent service':raise SystemExit('Refusing to reuse an unrelated account')
    public_runtime=None
    if args.public_proxy_user:
        if not re.fullmatch(r'[a-z_][a-z0-9_-]{1,30}',args.public_group):raise SystemExit('Invalid dedicated public group')
        proxy=pwd.getpwnam(args.public_proxy_user)
        if proxy.pw_uid==0 or proxy.pw_uid==account.pw_uid:raise SystemExit('Proxy must have a separate non-root identity')
        try:grp.getgrnam(args.public_group)
        except KeyError:run('groupadd','--system',args.public_group)
        for name in (args.user,args.public_proxy_user):run('usermod','--append','--groups',args.public_group,name)
        public_runtime=RUNTIME_ROOT/(args.unit+'-public')
    runtime=RUNTIME_ROOT/args.unit
    for path in (state,state/'workspace',state/'data'):
        if path.exists() and path.stat().st_uid!=account.pw_uid:raise SystemExit('State directory belongs to another user')
        path.mkdir(parents=True,exist_ok=True);os.chown(path,account.pw_uid,account.pw_gid);path.chmod(0o700)
    target.parent.mkdir(parents=True,exist_ok=True)
    if target.exists():
        for record in manifest['files']:
            path=target/record['path']
            if path.is_symlink() or not path.is_file() or hashlib.file_digest(path.open('rb'),'sha256').hexdigest()!=record['sha256']:
                raise SystemExit('Existing release differs; nothing overwritten')
    else:
        shutil.copytree(package,target)
        for path in target.rglob('*'):
            path.chmod(0o755 if path.is_dir() or path.relative_to(target).as_posix() in ('FreeAgent','bin/freeagent') else 0o644)
        target.chmod(0o755)
    if verify_package(target)!=manifest:
        refuse('Installed program copy differs from the verified package.', 'Keep the existing current program selected and review the failed release directory before retrying.')
    data=state/'data'/data_format;keys=state/'keys.json'
    env={**os.environ,'FREEAGENT_DATA_DIR':str(data),'FREEAGENT_KEYS_FILE':str(keys)}
    if not (data/'config.json').exists():
        run('runuser','-u',args.user,'--',str(target/'bin/freeagent'),'init','--workspace',str(state/'workspace'),env=env,stdout=subprocess.DEVNULL)
    run('runuser','-u',args.user,'--',str(target/'bin/freeagent'),'control','plan','--kind','server.set_policy','--arguments',json.dumps({'port':args.port}),env=env,input='y\n',text=True,stdout=subprocess.DEVNULL)
    if public_runtime:
        public=json.loads((data/'config.json').read_text())['public_web']
        public['socket']=str(public_runtime/'web.sock');public['socket_group']=args.public_group
        run('runuser','-u',args.user,'--',str(target/'bin/freeagent'),'control','plan','--kind','public_web.set_policy','--arguments',json.dumps(public),env=env,input='y\n',text=True,stdout=subprocess.DEVNULL)
    # Existing deployments must be stopped explicitly before changing binaries.
    if owned and subprocess.run(['systemctl','is-active','--quiet',args.unit]).returncode==0:
        raise SystemExit('Installed service is active; back up and stop it before switching programs')
    current=prefix/'current'
    if current.exists() and not current.is_symlink():raise SystemExit('Unknown current program path')
    staging=prefix/('.current-'+uuid.uuid4().hex)
    staged=False
    try:
        staging.symlink_to(target,target_is_directory=True)
        staged=True
        os.replace(staging,current)
    finally:
        if staged and staging.is_symlink():staging.unlink()
    executable=current/'bin/freeagent'
    text=f'''[Unit]
Description=FreeAgent single-user Harness ({args.unit})
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=3
[Service]
Type=exec
User={args.user}
Group={args.user}
StateDirectory={args.unit}
StateDirectoryMode=0700
RuntimeDirectory={args.unit}{' '+args.unit+'-public' if public_runtime else ''}
RuntimeDirectoryMode=0700
{('SupplementaryGroups='+args.public_group) if public_runtime else ''}
UMask=0077
Environment={systemd('FREEAGENT_DATA_DIR='+str(data))}
Environment={systemd('FREEAGENT_KEYS_FILE='+str(keys))}
WorkingDirectory={state}
ExecStart={systemd(executable)} web --service
Restart=on-failure
RestartSec=5
TimeoutStopSec=20
KillMode=control-group
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths={state} {runtime}{' '+str(public_runtime) if public_runtime else ''}
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
[Install]
WantedBy=multi-user.target
'''
    if unit.exists() and (not owned or unit.read_text()!=owned.get('unit_text')):
        refuse('Unit changed during installation.', 'Review the unit before retrying; it was not overwritten.')
    atomic_text(unit,text,0o644)
    command='env '+shlex.quote('FREEAGENT_DATA_DIR='+str(data))+' '+shlex.quote('FREEAGENT_KEYS_FILE='+str(keys))+' '+shlex.quote(str(executable))+' "$@"'
    wrapper=f'''#!/bin/sh
# FreeAgent managed server command: {args.unit}
set -eu
if [ "$(id -u)" = "{account.pw_uid}" ]; then exec {command}; fi
if [ "$(id -u)" != 0 ]; then echo 'Run as the FreeAgent service user or administrator.' >&2; exit 1; fi
exec runuser -u {shlex.quote(args.user)} -- {command}
'''
    if manager.exists() and (not owned or manager.read_text()!=owned.get('manager_text')):raise SystemExit('Management command has local edits; not overwritten')
    atomic_text(manager,wrapper,0o755)
    atomic_text(receipt,json.dumps({**identity,'unit_text':text,'manager_text':wrapper,'data_dir':str(data),'keys_file':str(keys)},indent=2)+'\n',0o600)
    run('systemctl','daemon-reload')
    run('systemd-analyze','verify',str(unit))
    if args.start:
        run('systemctl','enable','--now',args.unit)
        deadline=time.monotonic()+15
        owner_ready=False
        while time.monotonic()<deadline:
            try:
                ready=subprocess.run([str(manager),'owner','status'],capture_output=True,text=True,timeout=min(2,max(.1,deadline-time.monotonic())))
                if ready.returncode==0 and json.loads(ready.stdout).get('running'):
                    owner_ready=True
                    break
            except (subprocess.TimeoutExpired,ValueError,AttributeError):
                pass
            time.sleep(min(.25,max(0,deadline-time.monotonic())))
        if not owner_ready:
            refuse('Unit started but Owner readiness was not confirmed within 15 seconds.', 'Run '+str(manager)+' owner status and journalctl -u '+args.unit+'; stop the unit before retrying installation. Existing data was retained.')
    print(json.dumps({'installed':True,'unit':args.unit,'manager':str(manager),'data_dir':str(data),'started':args.start,
                      'warnings':checked['warnings'],'next_steps':[str(manager)+' doctor --text',str(manager)+' owner status','journalctl -u '+args.unit]}))


if __name__=='__main__':
    try:main()
    except (subprocess.CalledProcessError,OSError,ValueError,KeyError,TypeError) as error:
        raise SystemExit('Server installation did not finish; existing data was retained: '+str(error)+'\nNext: resolve the reported error, then rerun the same installer with --check before retrying. For a created service, inspect journalctl -u <unit>.')
