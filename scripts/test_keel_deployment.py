#!/usr/bin/env python3
"""Test deployment command construction with a mock SSH executable (no remote changes)."""
import os,pathlib,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory() as tmp:
 tmp=pathlib.Path(tmp);log=tmp/'log';ssh=tmp/'ssh'
 ssh.write_text('#!/bin/sh\ncat >/dev/null\nprintf "%s\\n" "$*" >> "$SSH_TEST_LOG"\n');ssh.chmod(0o755)
 env={**os.environ,'PATH':str(tmp)+os.pathsep+os.environ['PATH'],'SSH_TEST_LOG':str(log),'SSH_HOST':'test.example.com','SSH_USER':'deploy','SSH_PRIVATE_KEY':'test-key','SSH_KNOWN_HOSTS':'verified-key','DEPLOY_DIR':'/opt/switchboard','COMPOSE_PROJECT':'stable-project','KEEL_RUN_ID':'test-run-id','DATABASE_BACKEND':'postgres','POSTGRES_PASSWORD':'test-safe-password-1234567890','CONTROL_TOKEN':'test-control-token','ADMIN_PASSWORD':'test-admin-password','DOMAIN':'api.example.com','PUBLIC_URL':'https://api.example.com'}
 for stage in ['preflight','deploy','health']:subprocess.run(['python3',str(root/'deploy/ssh/deploy.py'),stage],env=env,stdin=subprocess.DEVNULL,check=True,timeout=30)
 text=log.read_text()
 assert 'StrictHostKeyChecking=yes' in text
 assert 'compose.postgres.yaml' in text and 'compose.tls.yaml' in text and 'stable-project' in text
 assert env['POSTGRES_PASSWORD'] not in text and env['CONTROL_TOKEN'] not in text
 assert '--wait' in text and 'healthz' in text
 assert '/releases/test-run-id' in text
 log.write_text('');env['DATABASE_BACKEND']='external-postgres';env['DATABASE_URL']='postgresql://user:test-secret@db.example.com/app?sslmode=require'
 subprocess.run(['python3',str(root/'deploy/ssh/deploy.py'),'deploy'],env=env,stdin=subprocess.DEVNULL,check=True,timeout=30)
 external=log.read_text();assert 'compose.postgres.yaml' not in external and 'test-secret' not in external
 print('Keel script: strict host verification, stable volumes, Compose overlays, health, and no secret argv passed')
