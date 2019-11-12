module github.com/kubernetes-incubator/external-storage/aws/efs

go 1.12

require (
	github.com/aws/aws-sdk-go v1.25.8
	github.com/miekg/dns v1.1.22 // indirect
	github.com/prometheus/client_golang v1.1.0 // indirect
	golang.org/x/crypto v0.0.0-20191002192127-34f69633bfdc // indirect
	golang.org/x/time v0.0.0-20190921001708-c4c64cad1fd0 // indirect
	k8s.io/api v0.0.0-20191003000013-35e20aa79eb8
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v0.0.0-20191003000419-f68efa97b39e
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20190923111123-69764acb6e8e // indirect
	sigs.k8s.io/sig-storage-lib-external-provisioner v4.0.1+incompatible
)
