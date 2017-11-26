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

package cmd

import (
	"flag"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"os"
	"strings"
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "iscsi-controller",
	Short: "an iscsi dynamic provisioner for kubernetes",
	Long: `an iscsi dynamic provisioner for kubernetes.	It requires targetd to be properly installed on the iscsi server`,
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	flag.CommandLine.Parse([]string{})
	cobra.OnInitialize(initConfig)

	//RootCmd.PersistentFlags().String("log-level", "info", "log level")
	//viper.BindPFlag("log-level", RootCmd.PersistentFlags().Lookup("log-level"))

	//	-logtostderr=false
	//		Logs are written to standard error instead of to files.
	//	-alsologtostderr=false
	//		Logs are written to standard error as well as to files.
	//	-stderrthreshold=ERROR
	//		Log events at or above this severity are logged to standard
	//		error as well as to files.
	//	-log_dir=""
	//		Log files will be written to this directory instead of the
	//		default temporary directory.
	//
	//	Other flags provide aids to debugging.
	//
	//	-log_backtrace_at=""
	//		When set to a file and line number holding a logging statement,
	//		such as
	//			-log_backtrace_at=gopherflakes.go:234
	//		a stack trace will be written to the Info log whenever execution
	//		hits that statement. (Unlike with -vmodule, the ".go" must be
	//		present.)
	//	-v=0
	//		Enable V-leveled logging at the specified level.
	//	-vmodule=""
	//		The syntax of the argument is a comma-separated list of pattern=N,
	//		where pattern is a literal file name (minus the ".go" suffix) or
	//		"glob" pattern and N is a V level. For instance,
	//			-vmodule=gopher*=3
	//		sets the V level to 3 in all Go files whose names begin "gopher".

}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// read in environment variables that match
	viper.AutomaticEnv()
}
