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

var ()

func main() {
	flag.Parse()

	// Start the NFS server
	startServer()
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		stopServer()
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

	err = provisionStatic(clientset, "/etc/config/exports.json")
	if err != nil {
		glog.Errorf("Error while provisioning static exports: %v", err)
	}

	nc := newNfsController(clientset, 15*time.Second)
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

// https://github.com/kubernetes/kubernetes/blob/release-1.4/examples/volumes/nfs/nfs-data/run_nfs.sh
func startServer() {
	glog.Error("Starting NFS")

	// Start rpcbind if it is not started yet
	cmd := exec.Command("/usr/sbin/rpcinfo", "127.0.0.1")
	if err := cmd.Run(); err != nil {
		glog.Errorf("Starting rpcbind")
		cmd := exec.Command("/usr/sbin/rpcbind", "-w")
		if err := cmd.Run(); err != nil {
			glog.Fatalf("Starting rpcbind failed: %v", err)
		}
	}

	// Mount the nfsd filesystem to /proc/fs/nfsd
	cmd = exec.Command("mount", "-t", "nfsd", "nfsd", "/proc/fs/nfsd")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Fatalf("mount nfsd failed with error: %v, output: %v", err, out)
	}

	// -N 4.x: disable NFSv4
	// -V 3: enable NFSv3
	cmd = exec.Command("/usr/sbin/rpc.mountd", "-N2", "-V3", "-N4", "-N4.1")
	if err := cmd.Run(); err != nil {
		glog.Fatalf("rpc.mountd failed: %v", err)
	}

	// -G 10 to reduce grace period to 10 seconds (the lowest allowed)
	cmd = exec.Command("/usr/sbin/rpc.nfsd", "-G10", "-N2", "-V3", "-N4", "-N4.1", "2")
	if err := cmd.Run(); err != nil {
		glog.Fatalf("rpc.nfsd failed: %v", err)
	}

	cmd = exec.Command("/usr/sbin/rpc.statd", "--no-notify")
	if err := cmd.Run(); err != nil {
		glog.Fatalf("rpc.statd failed: %v", err)
	}

	glog.Error("NFS started")
}

func stopServer() {
	glog.Error("Stopping NFS")

	cmd := exec.Command("/usr/sbin/rpc.nfsd", "0")
	if err := cmd.Run(); err != nil {
		glog.Errorf("rpc.nfsd failed: %v", err)
	}

	cmd = exec.Command("/usr/sbin/exportfs", "-au")
	if err := cmd.Run(); err != nil {
		glog.Errorf("exportfs -au failed: %v", err)
	}

	cmd = exec.Command("/usr/sbin/exportfs", "-f")
	if err := cmd.Run(); err != nil {
		glog.Errorf("exportfs -f failed: %v", err)
	}

	cmd = exec.Command("kill", "$( pidof rpc.mountd )")
	if err := cmd.Run(); err != nil {
		glog.Errorf("kill rpc.mountd failed: %v", err)
	}

	cmd = exec.Command("umount", "/proc/fs/nfsd")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("umount nfsd failed with error: %v, output: %v", err, out)
	}

	cmd = exec.Command("echo", ">", "/etc/exports")
	if err := cmd.Run(); err != nil {
		glog.Errorf("Cleaning /etc/exports failed: %v", err)
	}

	glog.Error("Stopped NFS")
}
