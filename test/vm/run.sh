#!/bin/bash
# Boots a distribution's own cloud image under KVM and runs the end-to-end
# tests inside it: test/vm/run.sh <system> <dir>, where <dir> holds the
# packages, the test binaries and Pebble. Runs on an Ubuntu CI runner.
set -euo pipefail
system=$1 dir=$(realpath "$2")
here=$(dirname "$(realpath "$0")")
work=${VM_WORK:-/tmp/vm}
mkdir -p "$work"

# find_in <directory URL> <regex>: the newest file name matching in a listing.
find_in() {
  curl -fsSL "$1" | grep -oE "$2" | sort -uV | tail -1 | sed "s|^|$1|"
}

case "$system" in
  debian-12) url=https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2 ;;
  debian-13) url=https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2 ;;
  fedora-43|fedora-44)
    v=${system#fedora-}
    url=$(find_in "https://dl.fedoraproject.org/pub/fedora/linux/releases/$v/Cloud/x86_64/images/" "Fedora-Cloud-Base-Generic-$v-[0-9.]+\.x86_64\.qcow2") ;;
  alma-9|alma-10)
    v=${system#alma-}
    url=https://repo.almalinux.org/almalinux/$v/cloud/x86_64/images/AlmaLinux-$v-GenericCloud-latest.x86_64.qcow2 ;;
  tumbleweed) url=https://download.opensuse.org/tumbleweed/appliances/openSUSE-Tumbleweed-Minimal-VM.x86_64-Cloud.qcow2 ;;
  leap-16)
    url=$(find_in "https://download.opensuse.org/distribution/leap/16.0/appliances/" "Leap-16\.0-Minimal-VM\.x86_64-[0-9.]*-?Cloud[^\"]*\.qcow2") ;;
  *) echo "unknown system $system"; exit 2 ;;
esac
echo "Image: $url"

base=$work/$(basename "$url")
[ -s "$base" ] || curl -fL --retry 3 -o "$base" "$url"
qemu-img create -q -f qcow2 -b "$base" -F qcow2 "$work/disk.qcow2" 40G

[ -f "$work/key" ] || ssh-keygen -q -t ed25519 -N "" -f "$work/key"
cat > "$work/user-data" <<CLOUD
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - name: root
    lock_passwd: true
    ssh_authorized_keys: ["$(cat "$work/key.pub")"]
CLOUD
echo "instance-id: ssm-$system" > "$work/meta-data"
cloud-localds "$work/seed.iso" "$work/user-data" "$work/meta-data"

qemu-system-x86_64 -enable-kvm -cpu host -smp "$(nproc)" -m 8192 \
  -drive file="$work/disk.qcow2",if=virtio -drive file="$work/seed.iso",if=virtio,format=raw \
  -netdev user,id=n0,hostfwd=tcp:127.0.0.1:2222-:22 -device virtio-net-pci,netdev=n0 \
  -display none -serial file:"$work/console.log" -pidfile "$work/qemu.pid" -daemonize

ssh_opts=(-i "$work/key" -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 -o ServerAliveInterval=30)
for i in $(seq 120); do
  ssh "${ssh_opts[@]}" root@127.0.0.1 true 2>/dev/null && break
  [ "$i" = 120 ] && { echo "the VM didn't come up"; tail -50 "$work/console.log"; exit 1; }
  sleep 5
done
ssh "${ssh_opts[@]}" root@127.0.0.1 'cloud-init status --wait >/dev/null 2>&1 || true; cat /etc/os-release | head -3'

tar -C "$dir" -cf - . | ssh "${ssh_opts[@]}" root@127.0.0.1 'mkdir -p /root/ci && tar -C /root/ci -xf -'
scp -q -P 2222 -i "$work/key" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$here/guest.sh" root@127.0.0.1:/root/ci/
set +e
ssh "${ssh_opts[@]}" root@127.0.0.1 'cd /root/ci && sh guest.sh'
status=$?
set -e
if [ $status != 0 ]; then
  ssh "${ssh_opts[@]}" root@127.0.0.1 'tail -n 100 /var/log/shiver-server-manager.log; journalctl -u "sharkord@*" -u caddy --no-pager -n 150; (ausearch -m avc -ts boot 2>/dev/null || journalctl -k --no-pager | grep -i avc) | tail -40' || true
fi
exit $status
