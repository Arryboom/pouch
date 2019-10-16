package main

import "github.com/spf13/cobra"

var (
	rootCmd = &cobra.Command{
		Use:          "quota_tool",
		Short:        "Set or get the quota information of path",
		Long:         "Set or get the quota information of path, also can set its sub-directory by recursively",
		SilenceUsage: true,
	}

	recursive bool
)

func main() {
	rootCmd.Execute()
}
