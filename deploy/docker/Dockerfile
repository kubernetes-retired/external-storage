# Modified from https://github.com/rootfs/nfs-ganesha-docker by Huamin Chen
FROM fedora:24

RUN dnf install -y tar gcc git cmake autoconf libtool bison flex make gcc-c++ krb5-devel dbus-devel dbus-x11 rpcbind hostname nfs-utils && dnf clean all \
	&& git clone --recursive https://github.com/nfs-ganesha/nfs-ganesha.git /nfs-ganesha \
	&& cd /nfs-ganesha \
	&& git reset --hard 0f55a9a97a4bf232fb0e42542e4ca7491fbf84ce \
	&& cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_CONFIG=vfs_only src/ \
	&& make \
	&& make install \
	&& cp src/scripts/ganeshactl/org.ganesha.nfsd.conf /etc/dbus-1/system.d/ \
	&& dnf remove -y tar gcc git cmake autoconf libtool bison flex make gcc-c++ krb5-devel dbus-devel && dnf clean all

RUN mkdir /var/run/dbus \
RUN mkdir -p /export \

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY vfs.conf /vfs.conf
COPY nfs-provisioner /nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
