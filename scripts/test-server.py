#!/usr/bin/env python3
"""Actual isolated systemd acceptance and optional wall-clock soak; no live Provider required."""
import argparse
import http.server
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import threading
import time
import urllib.error
import urllib.request


def require(value,message):
    if not value:raise RuntimeError(message)


def wait_for(check,message,timeout=20):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        value=check()
        if value:return value
        time.sleep(.1)
    raise RuntimeError(message)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manager',required=True)
    parser.add_argument('--unit',required=True)
    parser.add_argument('--state',required=True)
    parser.add_argument('--output',required=True)
    parser.add_argument('--soak-seconds',type=int,default=0)
    parser.add_argument('--functional-evidence',type=Path,
        help='Reuse a successful functional report for this exact service and run only a new complete soak')
    args=parser.parse_args()
    require(os.geteuid()==0,'Use the administrator for this isolated service acceptance')
    require(re.fullmatch(r'freeagent[a-z0-9_-]*',args.unit),'Invalid validation unit')
    require(args.soak_seconds==0 or args.soak_seconds>=28800,'An eight-hour soak requires at least 28800 actual seconds')
    require(not args.functional_evidence or args.soak_seconds,'Functional evidence requires a new soak')
    state=Path(args.state).resolve();require(state==Path('/var/lib')/args.unit,'State must belong to this validation unit')
    config=json.loads(next((state/'data').glob('*/config.json')).read_text())
    data=next((state/'data').glob('*/config.json')).parent
    output=Path(args.output);output.parent.mkdir(parents=True,exist_ok=True)
    result={'unit':args.unit,'data_dir':str(data),'checks':{},'real_provider':'NOT_RUN','real_channels':'NOT_RUN',
        'functional':'RUNNING','soak':'NOT_RUN','samples':[]}
    def save():
        temp=output.with_suffix('.tmp');temp.write_text(json.dumps({k:v for k,v in result.items() if not k.startswith('_')},indent=2)+'\n');temp.replace(output)
    def interrupted(signum,unused):
        raise RuntimeError('Acceptance interrupted by signal '+str(signum))
    signal.signal(signal.SIGTERM,interrupted)
    save()
    try:
        run_acceptance(args,state,data,config,result,save)
    except BaseException as error:
        if result['functional']=='RUNNING':result['functional']='FAIL'
        if result['soak']=='RUNNING':
            result['soak']='FAIL'
            result['actual_seconds']=round(time.monotonic()-result['_soak_monotonic'],3)
        result['failure']=type(error).__name__+': '+str(error)
        result['completed_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())
        save()
        raise
    print(json.dumps({k:v for k,v in result.items() if k!='samples' and not k.startswith('_')}))


def run_acceptance(args,state,data,config,result,save):
    workspace=Path(config['workspace'])
    def cli(*arguments,input_text='y\n',accepted=(0,),timeout=40):
        p=subprocess.run([args.manager,*arguments],input=input_text,capture_output=True,text=True,timeout=timeout)
        if p.returncode not in accepted:
            # Test fixtures never contain real credentials; emit only the bounded
            # user-facing failure, not discovery files or command environments.
            raise RuntimeError('CLI '+arguments[0]+' failed with exit '+str(p.returncode)+': '+(p.stderr or p.stdout)[-4096:])
        return json.loads(p.stdout) if p.stdout.strip() else None
    def sysctl(*arguments):return subprocess.run(['systemctl',*arguments,args.unit],check=True,capture_output=True,text=True)
    def owner():
        try:
            info=json.loads((data/'engine.json').read_text())
            key=(data/'chat.secret').read_text().strip()
            request=urllib.request.Request(info['origin']+'/api/environment',headers={'X-FreeAgent-Key':key})
            with urllib.request.urlopen(request,timeout=2) as response:
                if response.status==200:return info,key
        except (OSError,ValueError,urllib.error.URLError):return None
    def api(method,path,body=None,credential=None):
        info,key=credential or wait_for(owner,'Owner not ready')
        raw=json.dumps(body).encode() if body is not None else None
        headers={'X-FreeAgent-Key':key,'Origin':info['origin'],'Content-Type':'application/json'}
        request=urllib.request.Request(info['origin']+path,data=raw,headers=headers,method=method)
        with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
    def policy(kind,arguments):return cli('control','plan','--kind',kind,'--arguments',json.dumps(arguments),'--json')
    def soak():
        if not args.soak_seconds:return
        result.update(soak='RUNNING',_soak_monotonic=time.monotonic())
        save()
        # Dedicated conversations and a finite, explicit context limit keep this
        # lifecycle check independent of the functional fixture's tool history.
        prior=json.loads((data/'config.json').read_text())['max_messages']
        policy('runtime.set_limits',{'max_messages':256})
        try:
            cli('config','model','--id','echo','--provider','echo','--default')
            chats=[cli('run','--json','soak initialization '+str(i))['run']['conversation_id'] for i in range(2)]
            started=time.monotonic();deadline=started+args.soak_seconds;iteration=0
            result.update(soak='RUNNING',samples=[],actual_seconds=0,
                soak_started_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),
                soak_conversations=chats,_soak_monotonic=started)
            save()
            while time.monotonic()<deadline:
                if iteration%5==0:
                    chat=chats[(iteration//5)%2]
                    answer=cli('run','--session',chat,'--request','soak-'+str(iteration),'--json','soak '+str(iteration))
                    require(answer['run']['status']=='completed','Soak chat failed')
                metrics=subprocess.check_output(['systemctl','show',args.unit,'-p','MemoryCurrent','-p','CPUUsageNSec','-p','NRestarts'],text=True)
                require(subprocess.run(['systemctl','is-active','--quiet',args.unit]).returncode==0,'Soak service stopped')
                require(owner(),'Soak Owner did not respond')
                result['samples'].append({'elapsed_seconds':round(time.monotonic()-started,3),'metrics':metrics.strip()})
                result['actual_seconds']=round(time.monotonic()-started,3);save();iteration+=1
                time.sleep(min(60,max(0,deadline-time.monotonic())))
            result['actual_seconds']=round(time.monotonic()-started,3)
            require(result['actual_seconds']>=args.soak_seconds,'Soak duration was not reached')
        finally:
            # Cleanup is part of acceptance; a failed cleanup must not leave PASS.
            policy('runtime.set_limits',{'max_messages':prior})
        result['soak']='PASS'
    if args.functional_evidence:
        previous=json.loads(args.functional_evidence.read_text())
        require(previous.get('functional')=='PASS' and previous.get('unit')==args.unit and previous.get('data_dir')==str(data),
            'Functional evidence does not match this successful service validation')
        result['functional']='PASS';result['checks']=previous['checks']
        result['functional_evidence']=str(args.functional_evidence)
        wait_for(owner,'Service Owner did not become ready')
        result['owner_mode']=cli('owner','status')['mode']
        soak()
        result['completed_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime());save()
        return
    wait_for(owner,'Service Owner did not become ready')
    require(cli('owner','status')['mode']=='service','Owner does not report service mode')
    rootenv={**os.environ,'FREEAGENT_DATA_DIR':str(data),'FREEAGENT_KEYS_FILE':str(state/'keys.json')}
    executable=Path(os.readlink('/proc/'+str(cli('owner','status')['pid'])+'/exe'))
    denied=subprocess.run([str(executable),'doctor'],env=rootenv,capture_output=True,text=True)
    require(denied.returncode!=0 and 'another OS user' in denied.stderr,'Raw root CLI did not refuse foreign-owned data')
    result['checks']['nonroot_service_and_root_guard']=True
    require('ready'==cli('doctor')['sandbox']['status'],'Sandbox is unavailable inside the shipped unit')
    cli('config','model','--id','echo','--provider','echo','--default')
    chats=[cli('run','--json','service validation '+str(i))['run']['conversation_id'] for i in range(2)]
    cli('config','commands','--enabled','--allow','/usr/bin/dash','--timeout','15','--output-bytes','4096')
    command_policy=json.loads((data/'config.json').read_text())['commands']
    command_policy['rules']=[{'program':'/usr/bin/dash','decision':'allow'}]
    policy('commands.set_policy',command_policy)
    cli('spaces','permission','--space','local','--mode','workspace','--yes')
    shell='printf sandbox-proof > unit-sandbox.txt; if (printf bad > .git/commondir) 2>/dev/null; then exit 31; fi; if cat '+shlex.quote(str(data/'config.json'))+' 2>/dev/null; then exit 32; fi'
    proof=cli('commands','start','--session',chats[0],'--arguments',json.dumps({'command':'/usr/bin/dash','args':['-c',shell],'cwd':'.'}),'--interactive','--json')
    require((workspace/'unit-sandbox.txt').read_text()=='sandbox-proof','Unit sandbox did not persist workspace write')
    require(proof['evidence']['commands_run'][0]['backend']=='bubblewrap' and not proof['evidence']['commands_run'][0].get('host_escape_hatch',False),'Unit used a host fallback')
    require(not (workspace/'.git/commondir').exists(),'Unit sandbox changed Git metadata')
    result['checks']['unit_hardening_and_real_sandbox']=True

    # Stop the unit with a setsid descendant in its namespace.
    command={'command':'/usr/bin/dash','args':['-c',"setsid /bin/sh -c 'while true; do date +%s%N > unit-heartbeat; sleep .1; done' & wait"],'cwd':'.','background':True}
    background=cli('commands','start','--session',chats[0],'--arguments',json.dumps(command),'--interactive','--json')
    background_id=json.loads(background['answer'])['id']
    heartbeat=workspace/'unit-heartbeat';wait_for(lambda:heartbeat.exists(),'Unit child did not start')
    old=wait_for(owner,'Owner unavailable before stop')
    sysctl('stop')
    require(sysctl('show','-p','MainPID','--value').stdout.strip()=='0','systemctl stop left the main process')
    before=heartbeat.read_bytes();time.sleep(.3);require(heartbeat.read_bytes()==before,'systemctl stop left setsid child writing')
    cgroup=Path('/sys/fs/cgroup/system.slice')/(args.unit+'.service')/'cgroup.procs'
    require(not cgroup.exists() or not cgroup.read_text().strip(),'Stopped unit cgroup still has processes')
    sysctl('start');current=wait_for(owner,'Service did not restart')
    require(current[0]['generation']!=old[0]['generation'],'Restart reused the owner generation')
    try:api('GET','/api/environment',credential=old);raise RuntimeError('Old credential survived restart')
    except urllib.error.HTTPError as error:require(error.code==401,'Unexpected old-credential outcome')
    require(cli('commands','poll','--id',background_id,'--json')['state']!='running','Restart replayed an owned command')
    result['checks']['systemd_stop_sets_id_cleanup_and_restart']=True
    cli('spaces','permission','--space','local','--mode','full','--yes')
    command_policy=json.loads((data/'config.json').read_text())['commands']
    command_policy['services']={'enabled':True,'default_seconds':2,'max_total_seconds':30,'allow_local_listen':True}
    policy('commands.set_policy',command_policy)

    # A real HTTP dispatch is open when the Owner dies. Recovery must retain
    # UNKNOWN and must not ask the fixture a second time.
    opened=threading.Event();release=threading.Event();calls=[]
    class Provider(http.server.BaseHTTPRequestHandler):
        def log_message(self,*unused):pass
        def do_POST(self):
            self.rfile.read(int(self.headers.get('Content-Length','0')));calls.append(1)
            self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.flush();opened.set();release.wait(30)
    fixture=http.server.ThreadingHTTPServer(('127.0.0.1',0),Provider)
    threading.Thread(target=fixture.serve_forever,daemon=True).start()
    fixture_name='unit-crash-'+str(time.time_ns())
    cli('key','add','--name',fixture_name,'--url','http://127.0.0.1:'+str(fixture.server_port)+'/v1','--no-key','--enable')
    cli('config','model','--id','interrupted','--key',fixture_name,'--model','fixture')
    old_heartbeat=heartbeat.read_bytes()
    crash_background=cli('commands','start','--session',chats[1],'--arguments',json.dumps(command),'--interactive','--json')
    crash_background_id=json.loads(crash_background['answer'])['id']
    wait_for(lambda:heartbeat.read_bytes()!=old_heartbeat,'Crash background command did not start')
    cli('config','model','--id','interrupted','--default')
    process=subprocess.Popen([args.manager,'run','--session',chats[0],'--request','unit-crash-request','--json','controlled interruption'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        require(opened.wait(12),'Provider fixture was not actually dispatched')
        generation=wait_for(owner,'Owner unavailable')[0]['generation']
        sysctl('kill','--kill-who=main','--signal=KILL')
        wait_for(lambda:(value:=owner()) and value[0]['generation']!=generation,'on-failure restart did not produce a new generation',25)
        process.communicate(timeout=15)
        require(len(calls)==1,'Owner recovery replayed the model request')
        view=api('GET','/api/chats/'+chats[0])['view']
        turn=next((t for t in view['turns'] if t['request_key']=='unit-crash-request'),None)
        require(turn and turn['state']=='unknown','Interrupted Run did not retain UNKNOWN')
        inspected=cli('inspect','--run',turn['key'])
        attempts=inspected['attempts']
        require(any(a['kind']=='model' and a['status']=='unknown' for a in attempts),'Interrupted Attempt was replayed or lost')
        before=heartbeat.read_bytes();time.sleep(.3);require(heartbeat.read_bytes()==before,'Owner crash left a cgroup descendant writing')
        require(cli('commands','poll','--id',crash_background_id,'--json')['state']!='running','Crash command was replayed or still running')
        cli('abandon','--run',turn['key'])
    finally:
        release.set();fixture.shutdown();fixture.server_close()
        if process.poll() is None:process.kill();process.communicate()
    cli('config','model','--id','echo','--default')
    result['checks']['owner_crash_unknown_no_replay']=True

    # A short real service expiry is part of the soak; no accelerated clock.
    finite={'command':'/usr/bin/dash','args':['-c','printf READY; while true; do sleep .1; done'],'cwd':'.','mode':'service','duration_seconds':2,'ready_text':'READY','ready_timeout_seconds':1}
    service=cli('commands','start','--session',chats[0],'--arguments',json.dumps(finite),'--interactive','--json')
    finite_id=json.loads(service['answer'])['id']
    wait_for(lambda:cli('commands','poll','--id',finite_id,'--json')['state'] not in ('running','starting','stopping'),'Finite service did not expire',8)
    result['checks']['actual_finite_service_expiry']=True
    backup=state/'backups'/('unit-'+str(time.time_ns()))
    cli('backup','--out',str(backup));cli('backup-verify','--source',str(backup))
    cli('restore','--source',str(backup),'--data-dir',str(state/('restore-'+str(time.time_ns()))))
    result['checks']['online_backup_verify_and_restore']=True
    journal=subprocess.check_output(['journalctl','-u',args.unit,'--no-pager','-o','cat'],text=True)
    require('#key=' not in journal and wait_for(owner,'Owner unavailable')[1] not in journal,'Service journal contains a local capability')
    result['checks']['journal_without_launch_capability']=True
    result['functional']='PASS'
    save()
    soak()
    result['completed_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime());save()


if __name__=='__main__':main()
