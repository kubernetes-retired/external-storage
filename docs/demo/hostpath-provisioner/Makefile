.PHONY: image

IMAGE?=hostpath-provisioner

image: hostpath-provisioner
	docker build -t $(IMAGE) -f Dockerfile.scratch .

hostpath-provisioner: $(shell find . -name "*.go")
	glide install -v --strip-vcs
	CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o hostpath-provisioner .

.PHONY: clean
clean:
	rm hostpath-provisioner