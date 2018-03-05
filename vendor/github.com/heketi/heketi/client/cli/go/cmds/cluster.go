//
// Copyright (c) 2015 The heketi Authors
//
// This file is licensed to you under your choice of the GNU Lesser
// General Public License, version 3 or any later version (LGPLv3 or
// later), or the GNU General Public License, version 2 (GPLv2), in all
// cases as published by the Free Software Foundation.
//

package cmds

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/heketi/heketi/client/api/go-client"
	"github.com/heketi/heketi/pkg/glusterfs/api"
	"github.com/spf13/cobra"
)

var (
	cl_block bool
	cl_file  bool
)

func init() {
	RootCmd.AddCommand(clusterCommand)
	clusterCommand.AddCommand(clusterCreateCommand)
	clusterCommand.AddCommand(clusterDeleteCommand)
	clusterCommand.AddCommand(clusterListCommand)
	clusterCommand.AddCommand(clusterInfoCommand)

	clusterCreateCommand.Flags().BoolVar(&cl_block, "block", true,
		"\n\tOptional: Control the possibility of creating block volumes"+
			"\n\ton the cluster to be created. This is enabled by default."+
			"\n\tUse '--block=false' to disable creation of block volumes"+
			"\n\ton this cluster.")
	clusterCreateCommand.Flags().BoolVar(&cl_file, "file", true,
		"\n\tOptional: Control the possibility of creating regular file"+
			"\n\tvolumes on the cluster to be created. This is enabled by"+
			"\n\tdefault. Use '--file=false' to disable creation of file"+
			"\n\tvolumes on this cluster.")

	clusterCreateCommand.SilenceUsage = true
	clusterDeleteCommand.SilenceUsage = true
	clusterInfoCommand.SilenceUsage = true
	clusterListCommand.SilenceUsage = true
}

var clusterCommand = &cobra.Command{
	Use:   "cluster",
	Short: "Heketi cluster management",
	Long:  "Heketi Cluster Management",
}

var clusterCreateCommand = &cobra.Command{
	Use:   "create",
	Short: "Create a cluster",
	Long:  "Create a cluster",
	Example: `  * Create a normal cluster
      $ heketi-cli cluster create

  * Create a cluster only for file volumes:
      $ heketi-cli cluster create --block=false

  * Create a cluster only for block columes:
      $ heketi-cli cluster create --file=false
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		req := &api.ClusterCreateRequest{}
		req.File = cl_file
		req.Block = cl_block

		// Create a client to talk to Heketi
		heketi := client.NewClient(options.Url, options.User, options.Key)
		// Create cluster
		cluster, err := heketi.ClusterCreate(req)
		if err != nil {
			return err
		}

		// Check if JSON should be printed
		if options.Json {
			data, err := json.Marshal(cluster)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, string(data))
		} else {
			fmt.Fprintf(stdout, "Cluster id: %v\n", cluster.Id)
		}

		return nil
	},
}

var clusterDeleteCommand = &cobra.Command{
	Use:     "delete [cluster_id]",
	Short:   "Delete the cluster",
	Long:    "Delete the cluster",
	Example: "  $ heketi-cli cluster delete 886a86a868711bef83001",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := cmd.Flags().Args()

		//ensure proper number of args
		if len(s) < 1 {
			return errors.New("Cluster id missing")
		}

		//set clusterId
		clusterId := cmd.Flags().Arg(0)

		// Create a client
		heketi := client.NewClient(options.Url, options.User, options.Key)

		//set url
		err := heketi.ClusterDelete(clusterId)
		if err == nil {
			fmt.Fprintf(stdout, "Cluster %v deleted\n", clusterId)
		}

		return err
	},
}

var clusterInfoCommand = &cobra.Command{
	Use:     "info [cluster_id]",
	Short:   "Retrieves information about cluster",
	Long:    "Retrieves information about cluster",
	Example: "  $ heketi-cli cluster info 886a86a868711bef83001",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := cmd.Flags().Args()
		if len(s) < 1 {
			return errors.New("Cluster id missing")
		}

		//set clusterId
		clusterId := cmd.Flags().Arg(0)

		// Create a client to talk to Heketi
		heketi := client.NewClient(options.Url, options.User, options.Key)

		// Create cluster
		info, err := heketi.ClusterInfo(clusterId)
		if err != nil {
			return err
		}

		// Check if JSON should be printed
		if options.Json {
			data, err := json.Marshal(info)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, string(data))
		} else {
			fmt.Fprintf(stdout, "Cluster id: %v\n", info.Id)
			fmt.Fprintf(stdout, "Nodes:\n%v", strings.Join(info.Nodes, "\n"))
			fmt.Fprintf(stdout, "\nVolumes:\n%v", strings.Join(info.Volumes, "\n"))
			fmt.Fprintf(stdout, "\nBlock: %v\n", info.Block)
			fmt.Fprintf(stdout, "\nFile: %v\n", info.File)
		}

		return nil
	},
}

var clusterListCommand = &cobra.Command{
	Use:     "list",
	Short:   "Lists the clusters managed by Heketi",
	Long:    "Lists the clusters managed by Heketi",
	Example: "  $ heketi-cli cluster list",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Create a client
		heketi := client.NewClient(options.Url, options.User, options.Key)

		// List clusters
		list, err := heketi.ClusterList()
		if err != nil {
			return err
		}

		if options.Json {
			data, err := json.Marshal(list)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, string(data))
		} else {
			fmt.Fprintf(stdout, "Clusters:\n")
			for _, clusterid := range list.Clusters {
				cluster, err := heketi.ClusterInfo(clusterid)
				if err != nil {
					return err
				}

				usagestr := ""
				if cluster.File {
					usagestr = "[file]"
				}
				if cluster.Block {
					usagestr = usagestr + "[block]"
				}
				if usagestr == "" {
					usagestr = "[]"
				}

				fmt.Fprintf(stdout, "Id:%v %v\n", clusterid, usagestr)
			}
		}

		return nil
	},
}
