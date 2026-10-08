#!/usr/bin/env python3
"""Smoke test a freshly built non-root container and its remote CLI interface."""
import json,pathlib,secrets,subprocess,time,urllib.request
name='switchboard-smoke-'+secrets.token_hex(4)
token=secrets.token_urlsafe(32)
root=pathlib.Path(__file__).resolve().parent.parent
subprocess.run(['docker','run','--rm','-d','--name',name,'-p','127.0.0.1::8080','--mount','type=volume,destination=/data','-e','SWITCHBOARD_CONTROL_TOKEN='+token,'switchboard:local'],check=True,stdout=subprocess.DEVNULL)
try:
 port=subprocess.check_output(['docker','port',name,'8080'],text=True).strip().split(':')[-1]
 base='http://127.0.0.1:'+port
 for _ in range(100):
  try:
   with urllib.request.urlopen(base+'/healthz',timeout=1) as r:assert json.load(r)['status']=='ok'
   break
  except Exception:time.sleep(.2)
 else:raise RuntimeError('container did not become healthy')
 uid=subprocess.check_output(['docker','exec',name,'id','-u'],text=True).strip();assert uid=='10001',uid
 args=[str(root/'bin/switchboard'),'--url',base,'--admin-token',token]
 out=subprocess.check_output(args+['mcp','config','cursor','--json'],text=True)
 cfg=json.loads(out);assert cfg['url']==base+'/mcp' and cfg['token'].startswith('sba_')
 assert 'cursor' in subprocess.check_output(args+['tokens','list'],text=True)
 print('Container: non-root UID, fresh volume, healthy startup, and remote MCP credential creation passed')
finally:subprocess.run(['docker','stop','--time','10',name],check=True,stdout=subprocess.DEVNULL)
