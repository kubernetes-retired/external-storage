module github.com/kubernetes-incubator/external-storage/nfs-client

go 1.12

require (
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/miekg/dns v1.1.4 // indirect
	github.com/prometheus/client_golang v1.0.0 // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e // indirect
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	sigs.k8s.io/sig-storage-lib-external-provisioner v4.1.0+incompatible
)
