package main

import (
	"fmt"
	"os"

	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/storage/quota"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func init() {
	getSetupFlags(getCmd)
	rootCmd.AddCommand(getCmd)
}

func getSetupFlags(cmd *cobra.Command) {
	flagSet := cmd.PersistentFlags()

	flagSet.BoolVarP(&recursive, "recursive", "r", false, "get the directory quota id recursively. (Unsupported)")
}

var getCmd = &cobra.Command{
	Use:   "get <path>",
	Short: "Get quota information",
	Long:  `Get quota information`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := getRun(args); err != nil {
			log.With(nil).Error(err)
		}
	},
}

func getRun(args []string) error {
	path := args[0]
	if path == "" {
		return errors.Errorf("path can not be empty")
	} else if _, err := os.Stat(path); err != nil {
		return err
	}

	if recursive {
		fmt.Printf("Unsupported recursive")
		return nil
	}
	id := quota.GetQuotaIDInFileAttr(path)

	fmt.Printf("%d\n", id)

	return nil
}
