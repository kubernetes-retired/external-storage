/*
Copyright 2016 The Kubernetes Authors.

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
	"regexp"
	"strings"
)

var defaultGaneshaConfigContents = []byte(`
###################################################
#
# EXPORT
#
# To function, all that is required is an EXPORT
#
# Define the absolute minimal export
#
###################################################

EXPORT
{
	# Export Id (mandatory, each EXPORT must have a unique Export_Id)
	Export_Id = 0;

	# Exported path (mandatory)
	Path = /nonexistent;

	# Pseudo Path (required for NFS v4)
	Pseudo = /nonexistent;

	# Required for access (default is None)
	# Could use CLIENT blocks instead
	Access_Type = RW;

	# Exporting FSAL
	FSAL {
		Name = VFS;
	}
}

NFS_Core_Param
{
	MNT_Port = 20048;
}

NFSV4
{
	Grace_Period = 90;
}
`)

// Start starts the NFS server. If an error is encountered at any point it returns it instantly
func Start(ganeshaConfig string, gracePeriod uint) error {
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

	// Use defaultGaneshaConfigContents if the ganeshaConfig doesn't exist yet
	if _, err := os.Stat(ganeshaConfig); os.IsNotExist(err) {
		err = ioutil.WriteFile(ganeshaConfig, defaultGaneshaConfigContents, 0600)
		if err != nil {
			return fmt.Errorf("error writing ganesha config %s: %v", ganeshaConfig, err)
		}
	}
	err := setGracePeriod(ganeshaConfig, gracePeriod)
	if err != nil {
		return fmt.Errorf("error setting grace period to ganesha config: %v", err)
	}
	// Start ganesha.nfsd
	cmd = exec.Command("ganesha.nfsd", "-L", "/var/log/ganesha.log", "-f", ganeshaConfig)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ganesha.nfsd failed with error: %v, output: %s", err, out)
	}

	return nil
}

func setGracePeriod(ganeshaConfig string, gracePeriod uint) error {
	if gracePeriod > 180 {
		return fmt.Errorf("grace period cannot be greater than 180")
	}

	newLine := fmt.Sprintf("Grace_Period = %d;", gracePeriod)

	re := regexp.MustCompile("Grace_Period = [0-9]+;")

	read, err := ioutil.ReadFile(ganeshaConfig)
	if err != nil {
		return err
	}

	old := re.Find(read)

	if old == nil {
		// Grace_Period line not there, append the whole NFSV4 block.
		file, err := os.OpenFile(ganeshaConfig, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()

		block := "\nNFSV4\n{\n" +
			"\t" + newLine + "\n" +
			"}\n"

		if _, err = file.WriteString(block); err != nil {
			return err
		}
		file.Sync()
	} else {
		// Grace_Period line there, just replace it
		replaced := strings.Replace(string(read), string(old), newLine, -1)
		err = ioutil.WriteFile(ganeshaConfig, []byte(replaced), 0)
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop stops the NFS server.
func Stop() {
	// /bin/dbus-send --system   --dest=org.ganesha.nfsd --type=method_call /org/ganesha/nfsd/admin org.ganesha.nfsd.admin.shutdown
}
