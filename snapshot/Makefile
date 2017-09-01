.PHONY: all controller

all: controller

controller: 
	go build -o _output/bin/snapshot-controller cmd/snapshot-controller/snapshot-controller.go
	go build -o _output/bin/snapshot-provisioner cmd/snapshot-pv-provisioner/snapshot-pv-provisioner.go
