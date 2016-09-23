FROM centos
RUN yum -y install /usr/bin/ps nfs-utils sysvinit-tools && yum clean all
RUN mkdir -p /export

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY nfs-provisioner nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
