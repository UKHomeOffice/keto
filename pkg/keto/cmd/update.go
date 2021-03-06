/*
Copyright 2017 The Keto Authors

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
	"github.com/spf13/cobra"
)

// updateCmd represents the 'update' command
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update resources",
}

var updateClusterCmd = &cobra.Command{
	Use:          "cluster <NAME>",
	Aliases:      clusterCmdAliases,
	Short:        "Update a cluster",
	SilenceUsage: true,
	RunE: func(c *cobra.Command, args []string) error {
		return errNotImplemented
	},
}

var updateMasterPoolCmd = &cobra.Command{
	Use:          "masterpool <NAME>",
	Aliases:      masterPoolCmdAliases,
	Short:        "Update a masterpool",
	SilenceUsage: true,
	RunE: func(c *cobra.Command, args []string) error {
		return errNotImplemented
	},
}

var updateComputePoolCmd = &cobra.Command{
	Use:          "computepool <NAME>",
	Aliases:      computePoolCmdAliases,
	Short:        "Update a computepool",
	SilenceUsage: true,
	RunE: func(c *cobra.Command, args []string) error {
		return errNotImplemented
	},
}

func init() {
	updateCmd.AddCommand(
		updateClusterCmd,
		updateMasterPoolCmd,
		updateComputePoolCmd,
	)
}
