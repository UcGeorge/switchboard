#!/usr/bin/env python3
"""Exercise the real platform installer using release archives served from localhost."""
import functools, http.server, os, pathlib, platform, shutil, subprocess, tempfile, threading
root = pathlib.Path(__file__).resolve().parent.parent
system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows':'windows'}.get(platform.system())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64':'amd64'}.get(platform.machine().lower())
if not system or not arch: raise SystemExit('Unsupported test platform')
ext = 'zip' if system == 'windows' else 'tar.gz'
installer = ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(root/'scripts/install.ps1')] if system == 'windows' else ['sh',str(root/'scripts/install.sh')]
archives = list((root/'dist').glob(f'switchboard_*_{system}_{arch}.{ext}'))
if not archives: raise SystemExit('Build an archive for the host first with scripts/release.py')
archive = max(archives, key=lambda p: p.stat().st_mtime)
class Quiet(http.server.SimpleHTTPRequestHandler):
 def log_message(self, *args): pass
with tempfile.TemporaryDirectory() as tmp:
 tmp = pathlib.Path(tmp); release = tmp/'latest'/'download'; release.mkdir(parents=True)
 shutil.copy(archive, release/archive.name)
 import hashlib
 (release/'checksums.txt').write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
 server = http.server.ThreadingHTTPServer(('127.0.0.1',0), functools.partial(Quiet,directory=str(tmp)))
 threading.Thread(target=server.serve_forever,daemon=True).start()
 env = {**os.environ,'SWITCHBOARD_RELEASE_BASE_URL':f'http://127.0.0.1:{server.server_port}','SWITCHBOARD_INSTALL_DIR':str(tmp/'with spaces'/'bin'),'SWITCHBOARD_NO_PATH_UPDATE':'1'}
 try:
  subprocess.run(installer,env=env,check=True)
  binary = tmp/'with spaces'/'bin'/('switchboard.exe' if system == 'windows' else 'switchboard')
  subprocess.run([str(binary),'version'],check=True)
  before = binary.read_bytes()
  (release/'checksums.txt').write_text('0'*64+'  '+archive.name+'\n')
  res = subprocess.run(installer,env=env,capture_output=True,text=True)
  assert res.returncode != 0 and 'Checksum mismatch' in (res.stderr+res.stdout)
  assert binary.read_bytes() == before, 'failed verification changed existing install'
  print('Installer: verified install, spaced path, version execution, and corrupt-download rejection passed')
 finally: server.shutdown(); server.server_close()
