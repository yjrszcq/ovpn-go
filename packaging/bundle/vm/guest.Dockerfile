ARG BASE=debian:13-slim
FROM ${BASE}
ARG BUILDARCH
ARG TARGETARCH
# Imported Arch ARM roots need the official signing keyrings initialized before
# their first package transaction. Keep verification enabled and use a direct
# mirror in addition to the upstream geo-redirector.
RUN if command -v pacman >/dev/null; then \
      if [ ! -s /etc/pacman.d/gnupg/pubring.gpg ]; then pacman-key --init && pacman-key --populate || exit 1; fi; \
      if [ -f /usr/share/pacman/keyrings/archlinuxarm.gpg ]; then \
        sed -i '1iServer = https://mirror.math.princeton.edu/pub/archlinuxarm/$arch/$repo' /etc/pacman.d/mirrorlist; \
      fi; \
    fi
# User-mode QEMU cannot implement pacman's Landlock/seccomp download sandbox.
# Native builds retain it; this affects only the disposable guest image build.
RUN if command -v apk >/dev/null; then \
      apk add --no-cache openrc bash curl tar coreutils diffutils kmod python3; \
    elif command -v apt-get >/dev/null; then \
      apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends systemd-sysv udev dbus curl ca-certificates diffutils kmod python3 && rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null; then \
      dnf install -y systemd systemd-udev bash tar diffutils kmod python3 && dnf clean all; \
    else \
      sandbox=''; \
      if [ "$BUILDARCH" != "$TARGETARCH" ]; then sandbox=--disable-sandbox; fi; \
      pacman -Syu --noconfirm $sandbox systemd-sysvcompat bash curl tar coreutils diffutils kmod python3; fi
# Fail while building the guest, before any lengthy VM acceptance, if the
# distribution does not provide a tool used by the installer or its fixtures.
RUN for tool in bash curl tar sha256sum awk sed grep readlink seq find stat ln cmp python3 modprobe tee tail; do \
      command -v "$tool" || exit 1; \
    done
COPY fixtures/ /fixtures/
RUN if command -v rc-update >/dev/null; then \
      for service in devfs procfs sysfs; do rc-update add "$service" sysinit; done; \
      printf "auto lo\niface lo inet loopback\n" >/etc/network/interfaces && rc-update add networking boot && rc-update add bootmisc boot && rc-update add local default \
      && printf '#!/bin/sh\n/bin/bash /fixtures/vm-guest.sh\n' >/etc/local.d/ovpn-test.start && chmod 755 /etc/local.d/ovpn-test.start; \
    else \
      printf '[Unit]\nAfter=basic.target\n[Service]\nType=oneshot\nExecStart=/bin/bash /fixtures/vm-guest.sh\nTimeoutStartSec=1800\nStandardOutput=journal+console\nStandardError=journal+console\n[Install]\nWantedBy=multi-user.target\n' >/etc/systemd/system/ovpn-test.service \
      && systemctl enable ovpn-test.service; fi
# Imported rootfs images have no inherited Docker command.
CMD ["/sbin/init"]
