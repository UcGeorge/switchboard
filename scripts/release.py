#!/usr/bin/env python3
"""Build self-contained release archives and SHA-256 checksums. No release service required."""
import argparse, hashlib, os, pathlib, subprocess, tarfile, tempfile, zipfile
ROOT = pathlib.Path(__file__).resolve().parent.parent
p = argparse.ArgumentParser()
p.add_argument('--version', required=True)
p.add_argument('--target', action='append', help='os/arch; defaults to all supported targets')
a = p.parse_args()
version = a.version.removeprefix('v')
if not version or any(c not in '0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-' for c in version):
    p.error('invalid version')
targets = a.target or ['darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64', 'windows/arm64', 'windows/amd64']
try:
    commit = subprocess.check_output(['git', 'rev-parse', '--short', 'HEAD'], cwd=ROOT, stderr=subprocess.DEVNULL, text=True).strip()
except subprocess.CalledProcessError:
    commit = 'none'
from datetime import datetime, timezone
date = datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
dist = ROOT / 'dist'; dist.mkdir(exist_ok=True)
for target in targets:
    if target not in ['darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64', 'windows/arm64', 'windows/amd64']:
        p.error('unsupported target: ' + target)
    system, arch = target.split('/')
    with tempfile.TemporaryDirectory() as tmp:
        binary = 'switchboard.exe' if system == 'windows' else 'switchboard'
        output = pathlib.Path(tmp) / binary
        flags = f'-s -w -X github.com/ucgeorge/switchboard/internal/version.Version={version} -X github.com/ucgeorge/switchboard/internal/version.Commit={commit} -X github.com/ucgeorge/switchboard/internal/version.Date={date}'
        subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(output), './cmd/switchboard'], cwd=ROOT, env={**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'}, check=True)
        ext = 'zip' if system == 'windows' else 'tar.gz'
        archive = dist / f'switchboard_{version}_{system}_{arch}.{ext}'
        files = [(output, binary), (ROOT / 'LICENSE', 'LICENSE'), (ROOT / 'README.md', 'README.md'), (ROOT / 'THIRD_PARTY_NOTICES.md', 'THIRD_PARTY_NOTICES.md')]
        if system == 'windows':
            with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
                for src, name in files: z.write(src, name)
        else:
            with tarfile.open(archive, 'w:gz') as t:
                for src, name in files: t.add(src, arcname=name)
        print(archive.name, flush=True)
archives = sorted(dist.glob(f'switchboard_{version}_*.*'))
(dist / 'checksums.txt').write_text(''.join(hashlib.sha256(f.read_bytes()).hexdigest() + '  ' + f.name + '\n' for f in archives))
