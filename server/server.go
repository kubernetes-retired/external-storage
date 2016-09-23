package server

import (
	"os/exec"
	"strings"

	"github.com/golang/glog"
)

// Start is based on start in https://github.com/kubernetes/kubernetes/blob/release-1.4/examples/volumes/nfs/nfs-data/run_nfs.sh
// If an error is encountered at any point it returns it instantly
func Start() error {
	glog.Info("Starting NFS")

	// Start rpcbind if it is not started yet
	cmd := exec.Command("/usr/sbin/rpcinfo", "127.0.0.1")
	if err := cmd.Run(); err != nil {
		glog.Info("Starting rpcbind")
		cmd := exec.Command("/usr/sbin/rpcbind", "-w")
		if out, err := cmd.CombinedOutput(); err != nil {
			glog.Errorf("Starting rpcbind failed with error: %v, output: %s", err, out)
			return err
		}
	}

	// Mount the nfsd filesystem to /proc/fs/nfsd
	cmd = exec.Command("mount", "-t", "nfsd", "nfsd", "/proc/fs/nfsd")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("mount nfsd failed with error: %v, output: %s", err, out)
		return err
	}

	// -N 4.x: disable NFSv4
	// -V 3: enable NFSv3
	cmd = exec.Command("/usr/sbin/rpc.mountd", "-N2", "-V3", "-N4", "-N4.1")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("rpc.mountd failed with error: %v, output: %s", err, out)
		return err
	}

	// -G 10 to reduce grace period to 10 seconds (the lowest allowed)
	cmd = exec.Command("/usr/sbin/rpc.nfsd", "-G10", "-N2", "-V3", "-N4", "-N4.1", "2")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("rpc.nfsd failed with error: %v, output: %s", err, out)
		return err
	}

	cmd = exec.Command("/usr/sbin/rpc.statd", "--no-notify")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("rpc.statd failed with error: %v, output: %s", err, out)
		return err
	}

	glog.Info("NFS started")
	return nil
}

// Stop is based on stop in https://github.com/kubernetes/kubernetes/blob/release-1.4/examples/volumes/nfs/nfs-data/run_nfs.sh
func Stop() {
	glog.Info("Stopping NFS")

	cmd := exec.Command("/usr/sbin/rpc.nfsd", "0")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("rpc.nfsd failed with error: %v, output: %s", err, out)
	}

	cmd = exec.Command("/usr/sbin/exportfs", "-au")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("exportfs -au failed with error: %v, output: %s", err, out)
	}

	cmd = exec.Command("/usr/sbin/exportfs", "-f")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("exportfs -f failed with error: %v, output: %s", err, out)
	}

	cmd = exec.Command("/usr/sbin/pidof", "rpc.mountd")
	out, err := cmd.CombinedOutput()
	if err != nil {
		glog.Errorf("pidof rpc.mountd failed with error: %v, output: %s", err, out)
	}
	pid := strings.TrimSpace(string(out))
	cmd = exec.Command("kill", pid)
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("kill rpc.mountd failed with error: %v, output: %s", err, out)
	}

	cmd = exec.Command("umount", "/proc/fs/nfsd")
	if out, err := cmd.CombinedOutput(); err != nil {
		glog.Errorf("umount nfsd failed with error: %v, output: %s", err, out)
	}

	// TODO this is tied to 'static'; if we only do exportfs -o we never touch this
	// cmd = exec.Command("echo", ">", "/etc/exports")
	// if out, err := cmd.CombinedOutput(); err != nil {
	// 	glog.Errorf("Cleaning /etc/exports failed with error: %v, output: %s", err, out)
	// }

	glog.Info("Stopped NFS")
}
