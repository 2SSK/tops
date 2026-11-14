# A bare node for the helper tests: Ubuntu 24.04 with systemd, sshd and a deploy user with
# passwordless sudo, as the PRD requires of every node. Nothing of tops is installed.
FROM ubuntu:24.04@sha256:786a8b558f7be160c6c8c4a54f9a57274f3b4fb1491cf65146521ae77ff1dc54

ARG DEBIAN_FRONTEND=noninteractive

# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server sudo \
    && rm -rf /var/lib/apt/lists/*

RUN userdel -r ubuntu \
    && useradd --create-home --shell /bin/bash deploy \
    && echo 'deploy ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/deploy \
    && chmod 0440 /etc/sudoers.d/deploy \
    && visudo -c \
    && printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\n' > /etc/ssh/sshd_config.d/10-tops.conf \
    && ssh-keygen -A \
    && systemd-machine-id-setup

STOPSIGNAL SIGRTMIN+3

CMD ["/sbin/init"]
