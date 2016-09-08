FROM centos
RUN yum -y install /usr/bin/ps nfs-utils && yum clean all
RUN mkdir -p /exports

# expose mountd 20048/tcp and nfsd 2049/tcp
EXPOSE 2049/tcp 20048/tcp

COPY nfs nfs
COPY run_nfs.sh run_nfs.sh
ENTRYPOINT ["/nfs"]
