#!/usr/bin/env python3
"""Keel runner: strict SSH, source upload, remote Compose, health verification."""
import os,pathlib,shlex,subprocess,sys,tarfile,tempfile,time
root=pathlib.Path('/workspace') if pathlib.Path('/workspace').exists() else pathlib.Path(__file__).resolve().parents[2]
def ssh(command,stdin=None):
 return subprocess.run(['ssh','-p',os.environ.get('SSH_PORT','22'),'-i',str(key),'-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(hosts),'-o','BatchMode=yes',os.environ['SSH_USER']+'@'+os.environ['SSH_HOST'],command],input=stdin,check=True)
with tempfile.TemporaryDirectory() as tmp:
 key=pathlib.Path(tmp)/'key';hosts=pathlib.Path(tmp)/'known_hosts'
 key.write_text(os.environ['SSH_PRIVATE_KEY'].strip()+'\n');key.chmod(0o600);hosts.write_text(os.environ['SSH_KNOWN_HOSTS'].strip()+'\n')
 directory=os.environ.get('DEPLOY_DIR','/opt/switchboard');project=os.environ.get('COMPOSE_PROJECT','switchboard')
 prefix='set -eu; cd '+shlex.quote(directory)+'; '
 compose='docker compose -p '+shlex.quote(project)+' -f compose.yaml'
 if os.environ.get('DATABASE_BACKEND','postgres')=='postgres':compose+=' -f compose.postgres.yaml'
 domain=os.environ.get('DOMAIN','');url=os.environ.get('PUBLIC_URL','')
 if domain:
  if url!='https://'+domain:raise SystemExit('PUBLIC_URL must be https:// plus DOMAIN')
  compose+=' -f compose.tls.yaml'
 action=sys.argv[1]
 if action=='preflight':ssh(prefix+'docker version --format {{.Server.Version}} && docker compose version')
 elif action=='deploy':
  archive=pathlib.Path(tmp)/'source.tgz'
  with tarfile.open(archive,'w:gz') as tar:
   for name in ['Dockerfile','go.mod','go.sum','cmd','internal','compose.yaml','compose.postgres.yaml','compose.tls.yaml','deploy/Caddyfile']:tar.add(root/name,arcname=name)
  ssh(prefix+'tar -xzf -',archive.read_bytes())
  values={'SWITCHBOARD_CONTROL_TOKEN':os.environ['CONTROL_TOKEN'],'SWITCHBOARD_ADMIN_PASSWORD':os.environ['ADMIN_PASSWORD'],'SWITCHBOARD_DOMAIN':domain,'SWITCHBOARD_PUBLIC_URL':url,'POSTGRES_PASSWORD':os.environ.get('POSTGRES_PASSWORD','')}
  # Double-quoted dotenv values escape characters that Compose would interpret.
  def escape(s):return s.replace('\\','\\\\').replace('"','\\"').replace('$','$$').replace('\n','\\n').replace('\r','')
  env=''.join(k+'="'+escape(v)+'"\n' for k,v in values.items()).encode()
  ssh(prefix+'umask 077; cat > .env',env)
  ssh(prefix+compose+' up -d --build --wait --wait-timeout 180')
 elif action=='health':ssh(prefix+compose+' exec -T switchboard wget -qO- http://127.0.0.1:8080/healthz')
 else:raise SystemExit('expected preflight, deploy, or health')
