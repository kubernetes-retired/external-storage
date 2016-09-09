package main

import (
	"bufio"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"

	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/util/wait"
	"k8s.io/client-go/1.4/rest"
)

func main() {
	flag.Parse()

	startAndLog("/start_nfs.sh")
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		startAndLog("/stop_nfs.sh")
		os.Exit(1)
	}()

	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}
	nc := newNfsController(clientset, 5*time.Second)

	nc.Run(wait.NeverStop)
}

func startAndLog(command string) {
	cmd := exec.Command(command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		glog.Errorf("Stdout pipe error: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		glog.Fatalf("Error starting %s: %v", command, err)
	}
	in := bufio.NewScanner(stdout)
	for in.Scan() {
		glog.Errorf("%s: %v", command, in.Text())
	}
	if err := in.Err(); err != nil {
		glog.Errorf("Scanner error: %v", err)
	}
}
