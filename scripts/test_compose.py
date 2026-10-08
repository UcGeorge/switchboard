#!/usr/bin/env python3
"""Run both Compose backends, verify remote management/backup and persistent data."""
import json,os,pathlib,secrets,subprocess,tempfile,urllib.request
root=pathlib.Path(__file__).resolve().parent.parent
for backend in ['sqlite','postgres']:
 name='sb-smoke-'+secrets.token_hex(4);token=secrets.token_hex(32)
 with tempfile.TemporaryDirectory() as tmp:
  tmp=pathlib.Path(tmp);envfile=tmp/'values.env'
  envfile.write_text(f'SWITCHBOARD_PORT=0\nSWITCHBOARD_CONTROL_TOKEN={token}\nSWITCHBOARD_ADMIN_PASSWORD=test-only-password\nPOSTGRES_PASSWORD={secrets.token_hex(32)}\n')
  cmd=['docker','compose','--env-file',str(envfile),'-p',name,'-f',str(root/'compose.yaml')]
  if backend=='postgres':cmd+=['-f',str(root/'compose.postgres.yaml')]
  try:
   subprocess.run(cmd+['up','-d','--no-build','--wait','--wait-timeout','120'],check=True)
   port=subprocess.check_output(cmd+['port','switchboard','8080'],text=True).strip().split(':')[-1];base='http://127.0.0.1:'+port
   env={**os.environ,'SWITCHBOARD_NO_UPDATE_CHECK':'1','SWITCHBOARD_DATABASE_URL':'','DATABASE_URL':'','SWITCHBOARD_DB':'','SWITCHBOARD_URL':'','SWITCHBOARD_ADMIN_TOKEN':token}
   cli=[str(root/'bin/switchboard'),'--url',base]
   def run(*args):return subprocess.check_output(cli+list(args),env=env,text=True)
   run('keys','create','--name','persistent-smoke-key','--json')
   cfg=json.loads(run('mcp','config','cursor','--json'));assert cfg['token'].startswith('sba_')
   backup=tmp/'backup';run('db','backup',str(backup))
   magic=b'PGDMP' if backend=='postgres' else b'SQLite format 3';assert backup.read_bytes().startswith(magic)
   if backend=='postgres':
    subprocess.run(cmd+['exec','-T','postgres','createdb','-U','switchboard','verify_restore'],check=True)
    subprocess.run(cmd+['exec','-T','postgres','pg_restore','-U','switchboard','-d','verify_restore'],input=backup.read_bytes(),check=True)
    count=subprocess.check_output(cmd+['exec','-T','postgres','psql','-U','switchboard','-d','verify_restore','-Atc',"SELECT count(*) FROM api_keys WHERE name='persistent-smoke-key'"],text=True).strip()
    assert count=='1','restored data missing'
   else:
    assert 'persistent-smoke-key' in subprocess.check_output([str(root/'bin/switchboard'),'--db',str(backup),'keys','list'],env=env,text=True)

   subprocess.run(cmd+['restart','switchboard'],check=True)
   subprocess.run(cmd+['up','-d','--no-build','--wait','--wait-timeout','120'],check=True)
   port=subprocess.check_output(cmd+['port','switchboard','8080'],text=True).strip().split(':')[-1]
   base='http://127.0.0.1:'+port;cli=[str(root/'bin/switchboard'),'--url',base]
   assert 'persistent-smoke-key' in run('keys','list')
   print(f'{backend}: healthy startup, remote MCP token, native backup, restart persistence passed',flush=True)
  finally:
   # Only this script's randomly named, disposable test volumes are removed.
   subprocess.run(cmd+['down','-v'],check=True,stdout=subprocess.DEVNULL)
