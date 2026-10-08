#!/usr/bin/env python3
"""Exercise the real TUI in a fresh directory, then quit. macOS/Linux only."""
import fcntl, os, pathlib, pty, select, struct, sys, tempfile, termios, time, signal
binary=str(pathlib.Path(sys.argv[1] if len(sys.argv)>1 else 'bin/switchboard').resolve())
with tempfile.TemporaryDirectory() as tmp:
 data=pathlib.Path(tmp)/'not-created-yet'
 want_open='--open' in sys.argv[2:]
 shim=pathlib.Path(tmp)/'commands';shim.mkdir()
 if want_open:
  command='open' if sys.platform=='darwin' else 'xdg-open'
  program=shim/command
  program.write_text('#!/bin/sh\necho "$1" > "$SWITCHBOARD_BROWSER_MARKER"\n')
  program.chmod(0o755)
 pid,fd=pty.fork()
 if pid==0:
  os.environ['TERM']='xterm-256color';os.environ['SWITCHBOARD_DATA_DIR']=str(data)
  os.environ['PATH']=str(shim)+os.pathsep+os.environ['PATH']
  os.environ['SWITCHBOARD_BROWSER_MARKER']=str(pathlib.Path(tmp)/'opened-url')
  os.environ.pop('SWITCHBOARD_DB',None);os.environ.pop('SWITCHBOARD_URL',None)
  os.execv(binary,[binary,'--addr','127.0.0.1:0']+(['--open'] if want_open else []))
 fcntl.ioctl(fd,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0))
 output=b'';deadline=time.time()+15
 try:
  while time.time()<deadline and b'running' not in output:
   ready,_,_=select.select([fd],[],[],.2)
   if ready:
    try: b=os.read(fd,65536)
    except OSError: break
    output+=b
    if b'\x1b]11;?' in b:os.write(fd,b'\x1b]11;rgb:0b0b/0e0e/1414\x1b\\')
    if b'\x1b[6n' in b:os.write(fd,b'\x1b[1;1R')
  assert b'running' in output, 'fresh TUI failed to reach running state'
  if want_open:
   marker=pathlib.Path(tmp)/'opened-url'
   for _ in range(30):
    if marker.exists():break
    time.sleep(.1)
   assert marker.exists() and '/login?token=' in marker.read_text(),'browser launch did not receive login URL'
  assert (data/'switchboard.log').exists() and (data/'switchboard.db').exists()
  os.write(fd,b'q')
  deadline=time.time()+10;exited=False
  while time.time()<deadline:
   done,status=os.waitpid(pid,os.WNOHANG)
   if done:exited=True;assert os.waitstatus_to_exitcode(status)==0;break
   ready,_,_=select.select([fd],[],[],.1)
   if ready:
    try:b=os.read(fd,65536)
    except OSError:continue
    if b'\x1b]11;?' in b:os.write(fd,b'\x1b]11;rgb:0b0b/0e0e/1414\x1b\\')
    if b'\x1b[6n' in b:os.write(fd,b'\x1b[1;1R')
  assert exited,'TUI did not stop cleanly'
  print('Fresh TUI: created directory, log, database, rendered running state, and quit cleanly')
 finally:
  try:os.kill(pid,signal.SIGKILL)
  except ProcessLookupError:pass
  os.close(fd)
