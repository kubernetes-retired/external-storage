# Modified from https://github.com/rootfs/nfs-ganesha-docker by Huamin Chen
FROM fedora:24

RUN dnf install -y tar gcc git cmake autoconf libtool bison flex make gcc-c++ krb5-devel dbus-devel dbus-x11 rpcbind hostname nfs-utils && dnf clean all
RUN mkdir /var/run/dbus
RUN mkdir -p /export

RUN git clone --recursive https://github.com/nfs-ganesha/nfs-ganesha.git /nfs-ganesha
WORKDIR /nfs-ganesha
RUN cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_CONFIG=vfs_only src/
RUN make
RUN make install
RUN cp src/scripts/ganeshactl/org.ganesha.nfsd.conf /etc/dbus-1/system.d/

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY vfs.conf /vfs.conf
COPY nfs-provisioner /nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
