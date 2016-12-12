# Modified from https://github.com/rootfs/nfs-ganesha-docker by Huamin Chen
FROM fedora:24

# Build ganesha from source, installing deps and removing them in one line.
# Why?
# 1. Root_Id_Squash, only present in >= 2.4.0.3 which is not yet packaged
# 2. Set NFS_V4_RECOV_ROOT to /export

RUN dnf install -y tar gcc cmake autoconf libtool bison flex make gcc-c++ krb5-devel dbus-devel jemalloc libnfsidmap && dnf clean all \
	&& curl -L https://github.com/nfs-ganesha/nfs-ganesha/archive/V2.4.0.3.tar.gz | tar zx \
	&& curl -L https://github.com/nfs-ganesha/ntirpc/archive/v1.4.1.tar.gz | tar zx \
	&& rm -r nfs-ganesha-2.4.0.3/src/libntirpc \
	&& mv ntirpc-1.4.1 nfs-ganesha-2.4.0.3/src/libntirpc \
	&& cd nfs-ganesha-2.4.0.3 \
	&& cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_CONFIG=vfs_only src/ \
	&& sed -i 's|@SYSSTATEDIR@/lib/nfs/ganesha|/export|' src/include/config-h.in.cmake \
	&& make \
	&& make install \
	&& cp src/scripts/ganeshactl/org.ganesha.nfsd.conf /etc/dbus-1/system.d/ \
	&& dnf remove -y tar gcc cmake autoconf libtool bison flex make gcc-c++ krb5-devel dbus-devel jemalloc libnfsidmap && dnf clean all

RUN dnf install -y dbus-x11 rpcbind hostname nfs-utils xfsprogs

RUN mkdir -p /var/run/dbus
RUN mkdir -p /export

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY nfs-provisioner /nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
