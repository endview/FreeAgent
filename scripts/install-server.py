#!/usr/bin/env python3
"""Explicit Debian/systemd installation. Never removes user data or overwrites unknown units."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import pwd
import grp
import re
import shlex
import shutil
import socket
import subprocess
import time


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


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
    parser.add_argument('--public-proxy-user', help='explicitly prepare a restricted runtime directory for this existing TLS proxy user; does not enable public Web')
    parser.add_argument('--public-group', default='freeagent-public')
    args=parser.parse_args()
    if os.geteuid()!=0: raise SystemExit('Run the explicit installer as root; the installed service refuses root')
    for value in (args.user,args.unit):
        if not re.fullmatch(r'[a-z_][a-z0-9_-]{1,30}',value):raise SystemExit('Invalid user/unit name')
    if not 1<=args.port<=65535:raise SystemExit('Invalid loopback port')
    with socket.socket() as probe:
        try:probe.bind(('127.0.0.1',args.port))
        except OSError:raise SystemExit('Loopback port is occupied; choose a free port; no service was changed')
    package=Path(args.package).resolve();prefix=Path(args.prefix).absolute();manager=Path(args.manager).absolute()
    if prefix==Path('/') or not str(prefix).startswith('/opt/') or not str(manager).startswith('/usr/local/bin/'):
        raise SystemExit('Install prefix must be under /opt and manager under /usr/local/bin')
    for path in (prefix,manager):
        if path.is_symlink():raise SystemExit('Installation paths cannot be symbolic links')
        systemd(path)
    manifest=json.loads((package/'manifest.json').read_text())
    if manifest['target']!='linux/amd64' or manifest['source_dirty'] or not re.fullmatch(r'v[0-9][A-Za-z0-9.+-]*',manifest['candidate']) or not re.fullmatch(r'[0-9a-f]{40}',manifest['source_base_commit']):
        raise SystemExit('Use a clean Linux portable release candidate')
    data_format=manifest['data_format']
    if not re.fullmatch(r'Harness-v[0-9]+',data_format):raise SystemExit('Invalid current data format')
    for record in manifest['files']:
        path=package/record['path']
        if not path.resolve().is_relative_to(package) or not path.is_file() or path.is_symlink():raise SystemExit('Unsafe payload path')
        if hashlib.file_digest(path.open('rb'),'sha256').hexdigest()!=record['sha256']:raise SystemExit('Package integrity failure')
    if (package/'data').exists() or (package/'workspace').exists():raise SystemExit('Extract a clean package without user data')
    unit=Path('/etc/systemd/system')/(args.unit+'.service')
    receipt=prefix/'.freeagent-install.json'
    owned=json.loads(receipt.read_text()) if receipt.exists() else None
    if (unit.exists() or manager.exists() or prefix.exists()) and not owned:raise SystemExit('Unknown installation paths already exist; nothing overwritten')
    identity={'user':args.user,'unit':args.unit,'prefix':str(prefix),'manager':str(manager)}
    if owned and any(owned.get(k)!=v for k,v in identity.items()):raise SystemExit('Existing installation identity differs')
    if owned and unit.read_text()!=owned['unit_text']:raise SystemExit('Unit has local edits; review them before updating')
    if owned and manager.exists() and manager.read_text()!=owned.get('manager_text'):raise SystemExit('Management command has local edits; not overwritten')
    if owned and subprocess.run(['systemctl','is-active','--quiet',args.unit]).returncode==0:
        raise SystemExit('Installed service is active; back up and stop it before switching programs')
    try: account=pwd.getpwnam(args.user)
    except KeyError:
        run('useradd','--system','--user-group','--home-dir','/var/lib/'+args.unit,'--shell','/usr/sbin/nologin','--comment','FreeAgent service',args.user)
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
        public_runtime=Path('/run')/(args.unit+'-public')
    state=Path('/var/lib')/args.unit;runtime=Path('/run')/args.unit
    for path in (state,state/'workspace',state/'data'):
        if path.exists() and path.stat().st_uid!=account.pw_uid:raise SystemExit('State directory belongs to another user')
        path.mkdir(parents=True,exist_ok=True);os.chown(path,account.pw_uid,account.pw_gid);path.chmod(0o700)
    target=prefix/'releases'/(manifest['candidate']+'-'+manifest['source_base_commit'][:12])
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
    if current.is_symlink():current.unlink()
    current.symlink_to(target,target_is_directory=True)
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
    unit.write_text(text);unit.chmod(0o644)
    command='env '+shlex.quote('FREEAGENT_DATA_DIR='+str(data))+' '+shlex.quote('FREEAGENT_KEYS_FILE='+str(keys))+' '+shlex.quote(str(executable))+' "$@"'
    wrapper=f'''#!/bin/sh
# FreeAgent managed server command: {args.unit}
set -eu
if [ "$(id -u)" = "{account.pw_uid}" ]; then exec {command}; fi
if [ "$(id -u)" != 0 ]; then echo 'Run as the FreeAgent service user or administrator.' >&2; exit 1; fi
exec runuser -u {shlex.quote(args.user)} -- {command}
'''
    if manager.exists() and manager.read_text()!=owned.get('manager_text'):raise SystemExit('Management command has local edits; not overwritten')
    manager.write_text(wrapper);manager.chmod(0o755)
    receipt.write_text(json.dumps({**identity,'unit_text':text,'manager_text':wrapper,'data_dir':str(data),'keys_file':str(keys)},indent=2)+'\n');receipt.chmod(0o600)
    run('systemctl','daemon-reload')
    run('systemd-analyze','verify',str(unit))
    if args.start:
        run('systemctl','enable','--now',args.unit)
        for attempt in range(40):
            ready=subprocess.run([str(manager),'owner','status'],capture_output=True,text=True)
            if ready.returncode==0 and json.loads(ready.stdout).get('running'):break
            time.sleep(.25)
        else:raise SystemExit('Unit started but Owner readiness failed; inspect journalctl -u '+args.unit)
    print(json.dumps({'installed':True,'unit':args.unit,'manager':str(manager),'data_dir':str(data),'started':args.start}))


if __name__=='__main__':
    try:main()
    except (subprocess.CalledProcessError,OSError,ValueError,KeyError) as error:
        raise SystemExit('Server installation did not finish; existing data was retained: '+str(error))
