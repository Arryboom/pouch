package main

import (
	"os"

	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/storage/quota"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func init() {
	setupFlags(setCmd)
	rootCmd.AddCommand(setCmd)
}

var (
	size    string
	quotaID int
)

func setupFlags(cmd *cobra.Command) {
	flagSet := cmd.PersistentFlags()

	flagSet.StringVarP(&size, "size", "s", "", "The limit of directory")
	flagSet.IntVarP(&quotaID, "quota-id", "i", 0, "The quota id to set, -1 will clean quota id of directory")
	flagSet.BoolVarP(&recursive, "recursive", "r", false, "Set the directory recursively.")
}

var setCmd = &cobra.Command{
	Use:   "set <path>",
	Short: "Set quota",
	Long:  `Set quota information`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := setRun(args); err != nil {
			log.With(nil).Error(err)
		}
	},
}

func setRun(args []string) error {
	path := args[0]
	if path == "" {
		return errors.Errorf("path can not be empty")
	} else if _, err := os.Stat(path); err != nil {
		return err
	}

	var id uint32
	if quotaID == 0 {
		return errors.Errorf("quota id can not be 0")
	} else if quotaID == -1 {
		id = 0
	} else {
		id = uint32(quotaID)
	}

	if recursive {
		if size != "" {
			err := quota.SetDiskQuota(path, size, id)
			if err != nil {
				return errors.Wrapf(err, "failed to set subtree for %s, quota id: %d", path, id)
			}
		}
		if err := quota.SetFileAttrRecursive(path, id); err != nil {
			return errors.Wrapf(err, "failed to set quota id for %s recursively, quota id: %d", path, id)
		}
	} else {
		if size != "" {
			err := quota.SetDiskQuota(path, size, id)
			if err != nil {
				return errors.Wrapf(err, "failed to set subtree for %s, quota id: %d", path, id)
			}
		} else {
			if err := quota.SetQuotaIDInFileAttr(path, id); err != nil {
				return errors.Wrapf(err, "failed to set quota id for %s recursively, quota id: %d", path, id)
			}
		}
	}

	return nil
}
