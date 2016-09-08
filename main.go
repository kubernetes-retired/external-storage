package main

import (
	"flag"
	"os/exec"
	"time"

	"github.com/golang/glog"

	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/util/wait"
	"k8s.io/client-go/1.4/rest"
)

func main() {
	flag.Parse()

	cmd := exec.Command("/run_nfs.sh")
	err := cmd.Start()
	if err != nil {
		glog.Fatalf("Error starting run_nfs.sh: %v", err)
	}

	go func() {
		err := cmd.Wait()
		glog.Fatalf("run_nfs.sh stopped: %v", err)
	}()

	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("failed to create config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("failed to create client: %v", err)
	}
	nc := newNfsController(clientset, 5*time.Second)

	nc.Run(wait.NeverStop)
}
