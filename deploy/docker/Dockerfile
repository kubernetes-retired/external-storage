# Modified from https://github.com/rootfs/nfs-ganesha-docker by Huamin Chen
FROM fedora:24

RUN dnf install -y nfs-ganesha nfs-ganesha-vfs nfs-ganesha-utils dbus-x11 hostname
RUN mkdir /var/run/dbus
RUN mkdir -p /export

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY vfs.conf /vfs.conf
COPY nfs-provisioner /nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
