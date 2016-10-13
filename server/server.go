/*
Copyright 2016 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
)

const defaultGaneshaConfig = "/vfs.conf"

// Start starts the NFS server. If an error is encountered at any point it returns it instantly
func Start(ganeshaConfig string) error {
	// Start rpcbind if it is not started yet
	cmd := exec.Command("/usr/sbin/rpcinfo", "127.0.0.1")
	if err := cmd.Run(); err != nil {
		cmd := exec.Command("/usr/sbin/rpcbind", "-w")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Starting rpcbind failed with error: %v, output: %s", err, out)
		}
	}

	cmd = exec.Command("/usr/sbin/rpc.statd")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rpc.statd failed with error: %v, output: %s", err, out)
	}

	// Start dbus, needed for ganesha dynamic exports
	cmd = exec.Command("dbus-daemon", "--system")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dbus-daemon failed with error: %v, output: %s", err, out)
	}

	// Copy the default ganesha config to the export directory if one isn't there
	if _, err := os.Stat(ganeshaConfig); os.IsNotExist(err) {
		read, err := ioutil.ReadFile(defaultGaneshaConfig)
		if err != nil {
			return fmt.Errorf("error reading default ganesha config: %v", err)
		}
		err = ioutil.WriteFile(ganeshaConfig, read, 0600)
		if err != nil {
			return fmt.Errorf("error writing ganesha config: %v", err)
		}
	}
	// Start ganesha.nfsd
	cmd = exec.Command("ganesha.nfsd", "-L", "/var/log/ganesha.log", "-f", ganeshaConfig)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ganesha.nfsd failed with error: %v, output: %s", err, out)
	}

	return nil
}

// Stop stops the NFS server.
func Stop() {
	// /bin/dbus-send --system   --dest=org.ganesha.nfsd --type=method_call /org/ganesha/nfsd/admin org.ganesha.nfsd.admin.shutdown
}
