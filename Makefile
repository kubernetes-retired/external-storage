all: build

TAG = latest
PREFIX = wongma7/nfs-provisioner

build:
	go build

container: build
	cp nfs-provisioner deploy/docker/nfs-provisioner
	docker build -t $(PREFIX):$(TAG) deploy/docker

quick-container:
	cp nfs-provisioner deploy/docker/nfs-provisioner
	docker build -t $(PREFIX):$(TAG) deploy/docker

clean:
	rm -f nfs-provisioner
	rm -f deploy/docker/nfs-provisioner
