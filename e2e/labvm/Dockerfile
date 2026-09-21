FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11 AS guest
RUN apk add --no-cache linux-virt openssh btrfs-progs e2fsprogs iproute2 util-linux coreutils
COPY guest-init /init
RUN chmod 0755 /init && \
    mkdir -p /persist /data /run/sshd && \
    sed -i 's/^root:[^:]*:/root::/' /etc/shadow && \
    rm -f /boot/initramfs-* /etc/ssh/ssh_host_*

FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11
RUN apk add --no-cache qemu-system-aarch64 qemu-img openssh-client cpio gzip coreutils
COPY --from=guest / /guest/
COPY start-vm /usr/local/bin/start-vm
COPY guest-ssh /usr/local/bin/guest-ssh
RUN chmod 0755 /usr/local/bin/start-vm /usr/local/bin/guest-ssh && mkdir -p /vm /keys /payload
LABEL cvkeharness.test="recovery-vm-image"
ENTRYPOINT ["/usr/local/bin/start-vm"]
