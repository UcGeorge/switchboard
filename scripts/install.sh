#!/bin/sh
# Download a release, verify its checksum, then install without sudo.
set -eu
repo=${SWITCHBOARD_REPO:-ucgeorge/switchboard}
version=${SWITCHBOARD_VERSION:-latest}
dir=${SWITCHBOARD_INSTALL_DIR:-"$HOME/.local/bin"}
base=${SWITCHBOARD_RELEASE_BASE_URL:-"https://github.com/$repo/releases"}
case $(uname -s) in Darwin) os=darwin;; Linux) os=linux;; *) echo 'Use the PowerShell installer on Windows.' >&2; exit 1;; esac
case $(uname -m) in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) echo 'Unsupported CPU architecture.' >&2; exit 1;; esac
command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
command -v tar >/dev/null || { echo 'tar is required.' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if [ "$version" = latest ]; then url="$base/latest/download"; else
 case "$version" in *[!a-zA-Z0-9.-]*|'') echo 'Invalid version.' >&2; exit 1;; esac
 version=${version#v}; url="$base/download/v$version"
fi
curl -fsSL --retry 3 "$url/checksums.txt" -o "$tmp/checksums.txt"
# Discover the exact versioned filename from the checksum manifest.
asset=$(awk -v suffix="_${os}_${arch}.tar.gz" '$2 ~ /^switchboard_/ && substr($2,length($2)-length(suffix)+1)==suffix {print $2}' "$tmp/checksums.txt")
case "$asset" in ''|*/*|*' '..* ) echo 'Invalid release manifest.' >&2; exit 1;; esac
[ "$(printf '%s\n' "$asset" | wc -l | tr -d ' ')" = 1 ] || { echo 'Ambiguous release manifest.' >&2; exit 1; }
expected=$(awk -v a="$asset" '$2==a {print $1}' "$tmp/checksums.txt")
curl -fsSL --retry 3 "$url/$asset" -o "$tmp/$asset"
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || { echo 'Checksum mismatch: nothing installed.' >&2; exit 1; }
tar -xzf "$tmp/$asset" -C "$tmp" switchboard
mkdir -p "$dir"
install -m 755 "$tmp/switchboard" "$dir/.switchboard-new"
mv -f "$dir/.switchboard-new" "$dir/switchboard"
printf 'Installed %s\n' "$dir/switchboard"
case ":$PATH:" in *":$dir:"*) ;; *) printf 'Add to your shell profile: export PATH="%s:$PATH"\n' "$dir";; esac
"$dir/switchboard" version
