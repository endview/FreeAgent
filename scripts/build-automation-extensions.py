#!/usr/bin/env python3
"""Build optional automation packages; no installation, enablement or downloads."""
import argparse,hashlib,json,os,shutil,subprocess,sys,zipfile,tarfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--target',choices=['windows-amd64','linux-amd64'],required=True);p.add_argument('--go',default='go');p.add_argument('--out',required=True);a=p.parse_args()
 out=Path(a.out).resolve();out.mkdir(parents=True,exist_ok=True)
 module=ROOT/'extensions/automation';env=dict(os.environ,GOOS=a.target.split('-')[0],GOARCH='amd64',CGO_ENABLED='0');suffix='.exe' if a.target.startswith('windows') else ''
 ids=['browser']+(['computer-use'] if suffix else [])
 for name in ids:
  package=out/('cc.endview.'+name);package.mkdir();shutil.copy2(module/name/'extension.json',package/'extension.json')
  binary=package/('freeagent-'+name+suffix)
  if name=='browser':subprocess.run([a.go,'build','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',str(binary),'./cmd/browser'],cwd=module,env=env,check=True)
  else:
   net=Path(os.environ.get('WINDIR','C:/Windows'))/'Microsoft.NET/Framework64/v4.0.30319';csc=net/'csc.exe';assert csc.exists(),'Windows .NET Framework compiler is required'
   refs=['System.Web.Extensions.dll','System.Windows.Forms.dll','System.Drawing.dll',str(net/'WPF/WindowsBase.dll'),str(net/'WPF/UIAutomationClient.dll'),str(net/'WPF/UIAutomationTypes.dll')]
   subprocess.run([str(csc),'/nologo','/optimize+','/target:exe','/platform:x64','/out:'+str(binary),*['/r:'+r for r in refs],str(module/'computer-use/ComputerUse.cs')],cwd=ROOT,check=True)
  manifest=json.loads((package/'extension.json').read_text(encoding='utf-8'));manifest['run']['command']='./'+binary.name;(package/'extension.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
  shutil.copy2(module/'README.md',package/'README.md');shutil.copy2(ROOT/'LICENSE',package/'LICENSE')
  metadata={'extension':manifest['id'],'version':manifest['version'],'target':a.target,'minimum_host':'v8.9.0-dev.2','source_base_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'source_dirty':bool(subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True).strip()),'bundled_credentials':False,'bundled_user_data':False,'bundled_browser':False}
  (package/'BUILD_INFO.json').write_text(json.dumps(metadata,indent=2)+'\n',encoding='utf-8')
  if name=='browser':
   goinfo=json.loads(subprocess.check_output([a.go,'list','-m','-json','github.com/gorilla/websocket'],cwd=module,encoding='utf-8'));shutil.copy2(Path(goinfo['Dir'])/'LICENSE',package/'WEBSOCKET-LICENSE')
   goinfo=json.loads(subprocess.check_output([a.go,'list','-m','-json','golang.org/x/sys'],cwd=module,encoding='utf-8'));shutil.copy2(Path(goinfo['Dir'])/'LICENSE',package/'GO-SYS-LICENSE')
  inventory={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(package.iterdir()) if f.is_file()};(package/'SHA256SUMS.txt').write_text(''.join(v+'  '+k+'\n' for k,v in inventory.items()),encoding='utf-8')
  archive=out/(name+'-v'+manifest['version']+'-'+a.target+('.zip' if suffix else '.tar.gz'))
  if suffix:
   with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
    for f in sorted(package.iterdir()):z.write(f,package.name+'/'+f.name)
  else:
   with tarfile.open(archive,'w:gz') as t:
    for f in sorted(package.iterdir()):
     info=t.gettarinfo(f,arcname=package.name+'/'+f.name);info.mode=0o755 if f==binary else 0o644
     with f.open('rb') as stream:t.addfile(info,stream)
  print(json.dumps({'extension':manifest['id'],'version':manifest['version'],'target':a.target,'archive':str(archive),'sha256':hashlib.sha256(archive.read_bytes()).hexdigest()}),flush=True)
if __name__=='__main__':main()
