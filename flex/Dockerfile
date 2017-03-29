# Copyright 2015 bradley childs, All rights reserved.
#

FROM centos:7
MAINTAINER bradley childs, bchilds@gmail.com
# AWS CLI build deps
RUN yum update -y

# Provisioner build deps

# install the shell flex script that the provisioner uses.
RUN mkdir -p  /opt/go/src/kubernetes-incubator/external-storage/flex/flex-script
COPY flex-script/flex/flex /opt/go/src/kubernetes-incubator/external-storage/flex/flex-script

# install the go kube piece of provisioner
RUN mkdir -p  /opt/go/src/kubernetes-incubator/external-storage/flex/
COPY flex /opt/go/src/kubernetes-incubator/external-storage/flex/


ENTRYPOINT ["/opt/go/src/kubernetes-incubator/external-storage/flex/flex"]

# "github.com/kubernetes-incubator/external-storage/lib/controller"