#!/bin/sh
# Installs the package from <dir> in a bare container the way an admin
# would, checks the files the install scripts make, then removes it.
set -eux
dir=$(cd "$1" && pwd)
export DEBIAN_FRONTEND=noninteractive
. /etc/os-release
case " $ID ${ID_LIKE:-} " in
  *" debian "*|*" ubuntu "*)
    apt-get update
    # A bare container: recommends (cryptsetup) pull in interactive setup.
    apt-get install -y --no-install-recommends "$dir"/shiver-server-manager_*.deb sudo
    remove="apt-get remove -y shiver-server-manager"
    purge="apt-get purge -y shiver-server-manager" ;;
  *suse*)
    zypper -n install sudo
    zypper -n install --allow-unsigned-rpm "$dir"/shiver-server-manager-*.rpm
    remove="zypper -n remove shiver-server-manager"
    purge=true ;;
  *)
    dnf -y install sudo
    dnf -y install "$dir"/shiver-server-manager-*.rpm
    remove="dnf -y remove shiver-server-manager"
    purge=true ;;
esac
getent group shiver-admins
test "$(stat -c %a /var/lib/shiver-server-manager)" = 711
test "$(stat -c %a /var/lib/shiver-server-manager/vault)" = 700
test "$(stat -c %a /etc/sudoers.d/shiver-server-manager)" = 440
# The PAM file only includes stacks this system has.
cat /etc/pam.d/shiver-server-manager
for f in $(sed -n 's/^[a-z-]*[[:space:]]*include[[:space:]]*\([^[:space:]]*\).*/\1/p; s/^@include[[:space:]]*//p' /etc/pam.d/shiver-server-manager); do
  test -f "/etc/pam.d/$f"
done
visudo -cf /etc/sudoers.d/shiver-server-manager
shiver-server-manager version
useradd -m -G shiver-admins ci-admin
su ci-admin -s /bin/sh -c "sudo -n /usr/bin/shiver-server-manager version"
$remove
$purge
test -d /var/lib/shiver-server-manager
