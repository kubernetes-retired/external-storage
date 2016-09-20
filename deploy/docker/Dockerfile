FROM centos
RUN yum -y install /usr/bin/ps nfs-utils && yum clean all
RUN mkdir -p /exports

# expose mountd 20048/tcp and nfsd 2049/tcp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY nfs-provisioner nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
