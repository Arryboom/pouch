package mgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/storage/quota"
	"github.com/sirupsen/logrus"
)

var (
	minQuota = int64(quota.QuotaMinID)
)

type diskQuotaRegExp struct {
	pattern   *regexp.Regexp
	limitSize string
}

func (mgr *ContainerManager) fixQuotaProcess(ctx context.Context, c *Container, config *types.UpdateConfig) error {

	// check value is valid
	if config.FixQuotaContainer != 0 {
		if config.FixQuotaContainer != -1 && config.FixQuotaContainer < minQuota {
			return fmt.Errorf("can not fix quota with id %d", config.FixQuotaContainer)
		}
	}
	if config.FixQuotaRootfs != 0 {
		if config.FixQuotaRootfs != -1 && config.FixQuotaRootfs < minQuota {
			return fmt.Errorf("can not fix quota with id %d", config.FixQuotaRootfs)
		}
	}
	if config.FixQuotaVolumes != 0 {
		if config.FixQuotaVolumes != -1 && config.FixQuotaVolumes < minQuota {
			return fmt.Errorf("can not fix quota with id %d", config.FixQuotaVolumes)
		}
	}

	/*
		if !local.UseQuota {
			return fmt.Errorf("failed to fix quota, fs not support quota")
		}
	*/

	// label DiskQuota must have
	diskQuota, ok := c.Config.Labels["DiskQuota"]
	if !ok || len(diskQuota) == 0 {
		return fmt.Errorf("failed to fix quota, label DiskQuota not set")
	}

	// the order should be same with one in api/client/update.go
	if config.FixQuotaContainer != 0 {
		log.With(ctx).Infof("do quota fix, fix container all quota with quota id %d", config.FixQuotaContainer)
		return mgr.FixContainerQuota(ctx, c, config.FixQuotaContainer)
	} else if config.FixQuotaRootfs != 0 {
		log.With(ctx).Infof("do quota fix, fix container rootfs quota with quota id %d", config.FixQuotaRootfs)
		return mgr.FixContainerRootfsQuota(ctx, c, config.FixQuotaRootfs)
	} else if config.FixQuotaVolumes != 0 {
		log.With(ctx).Infof("do quota fix, fix container volumes quota with quota id %d", config.FixQuotaVolumes)
		return mgr.FixContainerVolumesQuota(ctx, c, config.FixQuotaVolumes)
	}

	log.With(ctx).Infof("null quota is fixed, return")
	return nil

}

// FixContainerQuota fix container all quota
// NOTE: fix container only do in 1 condition
// 1. passed quota id = -1, update container quota id as origin quota id
func (mgr *ContainerManager) FixContainerQuota(ctx context.Context, c *Container, fixedQuota int64) error {
	if fixedQuota != -1 {
		return fmt.Errorf("only support fix container quota with origin quota")
	}

	dqre := parseDiskQuota(c.Config.DiskQuota)
	if len(dqre) > 1 {
		return fmt.Errorf("failed to fix container quota with DiskQuota %v", c.Config.DiskQuota)
	}

	quotaid, err := strconv.Atoi(c.Config.QuotaID)
	if quotaid == 0 || err != nil {
		return fmt.Errorf("failed to fix container quota cause cannot get quota id from config: %s", err)
	}
	qid := uint32(quotaid)
	mounted := false
	allMounts, err := mgr.getDiskQuotaMountPoints(ctx, c, mounted)
	if err != nil {
		return fmt.Errorf("failed to fix container quota cause cannot get mountpoints %s", err)
	}

	for _, m := range allMounts {
		dest := m.Destination
		var src string
		var size string
		for _, re := range dqre {
			matched := re.pattern.FindString(dest)
			if matched == dest {
				src = m.Source
				size = re.limitSize
				break
			}
		}
		if len(src) == 0 || len(size) == 0 {
			continue
		}

		if dest == "/" {
			// only update upper here
			upper := c.Snapshotter.Data["UpperDir"]
			if upper == "" {
				log.With(ctx).Warnf("failed to get upper(%s) info in fix quota", src)
				continue
			}
			src = upper
		}
		log.With(ctx).Infof("fix container directiry %s quota", src)
		if err = quota.SetDiskQuota(src, size, qid); err != nil {
			log.With(ctx).Warnf("failed to set %s quota,  size(%s), quota id(%d), err(%v)", src, size, qid, err)
		}

		go quota.SetFileAttrRecursive(src, qid)
	}

	return nil
}

// FixContainerRootfsQuota fixs container rootfs
// NOTE: fix container rootfs quota id, in two condition
// 1. fixedQuota = -1, flush rootfs quota id according to current quota id
// 2. fixedQuota > 16777216, flush rootfs quota id with fixedQuota
func (mgr *ContainerManager) FixContainerRootfsQuota(ctx context.Context, c *Container, fixedQuota int64) error {
	if fixedQuota != -1 && fixedQuota < minQuota {
		return fmt.Errorf("failed to fix rootfs quota with id %d", fixedQuota)
	}

	dqre := parseDiskQuota(c.Config.DiskQuota)
	size := ""
	for _, re := range dqre {
		matched := re.pattern.FindString("/")
		if matched == "/" {
			size = re.limitSize
			break
		}
	}

	if size == "" {
		return fmt.Errorf("failed to fix container quota, can not get quota limit")
	}

	// first get quota from label, then get from upper directiry
	labelQid, _ := strconv.Atoi(c.Config.QuotaID)

	upper := c.Snapshotter.Data["UpperDir"]
	if labelQid == 0 && upper == "" {
		return fmt.Errorf("failed to fix container rootfs quota id, quota id get from label is 0 and upper directory not have quotaid")
	}

	existQid := int(quota.GetQuotaIDInFileAttr(upper))
	if labelQid != 0 && existQid != 0 && existQid != labelQid && fixedQuota == -1 {
		return fmt.Errorf("failed to fix container rootfs quota id, quota id get from label(%d) and directory(%d) not equal, please specified quota id with --fix-quota-rootfs", labelQid, existQid)
	}

	qid := int(fixedQuota)
	if qid == -1 {
		qid = labelQid
	}

	if qid == 0 {
		qid = existQid
	}

	if qid == 0 {
		return fmt.Errorf("failed to fix container rootfs quota id, can not get from fixedQuota passed, label, directory, better to specified quota id with --fix-quota-rootfs")
	}

	if err := quota.SetDiskQuota(upper, size, uint32(qid)); err != nil {
		log.With(ctx).Warnf("failed to set rootfs quota, mountfs(%s), size(%s), quota id(%d), err(%v)", upper, size, qid, err)
	}
	go quota.SetFileAttrRecursive(upper, uint32(qid))

	return nil
}

// FixContainerVolumesQuota fixs container all volume quota
// NOTE: fix container all volumes quotaid, only local driver and container use one diskquota,
// like 60g or /60g
// 1. fixedQuota = -1, flush rootfs quota id according to current quota id
// 2. fixedQuota > 16777216, flush rootfs quota id with fixedQuota
func (mgr *ContainerManager) FixContainerVolumesQuota(ctx context.Context, c *Container, fixedQuota int64) error {
	if fixedQuota != -1 && fixedQuota < minQuota {
		return fmt.Errorf("failed to fix rootfs quota with id %d", fixedQuota)
	}

	mountPoints := make(map[string]string)
	for _, m := range c.Mounts {
		if m.Driver != "local" {
			continue
		}

		path := m.Source
		if "" == path {
			continue
		}
		finfo, err := os.Stat(path)
		if err != nil || !finfo.IsDir() {
			continue
		}
		mountPoints[filepath.Clean(m.Destination)] = path
	}

	if len(mountPoints) == 0 {
		log.With(ctx).Infof("no volume need to fix quota, return")
		return nil
	}

	dqre := parseDiskQuota(c.Config.DiskQuota)
	if len(dqre) > 1 {
		return fmt.Errorf("failed to fix container quota with DiskQuota %v", c.Config.DiskQuota)
	}

	size := ""
	for _, re := range dqre {
		matched := re.pattern.FindString("/")
		if matched == "/" {
			size = re.limitSize
			break
		}
	}

	if size == "" {
		return fmt.Errorf("failed to fix container quota, can not get quota limit")
	}

	// only get quota from label
	labelQid, _ := strconv.Atoi(c.Config.QuotaID)
	qid := int(fixedQuota)
	if qid == -1 {
		qid = labelQid
	}

	if qid == 0 {
		return fmt.Errorf("failed to fix container volume quota id, can not get from fixedQuota passed, label, better to specified quota id with --fix-quota-rootfs")
	}

	log.With(ctx).Infof("fix container all volume %+v with quota %d", mountPoints, qid)

	for _, value := range mountPoints {
		logrus.Infof("fix container volume %s with quota id %d", value, qid)
		if err := quota.SetDiskQuota(value, size, uint32(qid)); err != nil {
			logrus.Errorf("failed to fix container volume %s with quota id %d, setQuota error: %s", value, qid, err)
		}
		go quota.SetFileAttrRecursive(value, uint32(qid))
	}

	return nil
}

func parseDiskQuota(quota map[string]string) []*diskQuotaRegExp {
	dqre := make([]*diskQuotaRegExp, 0)
	for exp, size := range quota {
		if re, err := regexp.Compile(exp); err == nil {
			dqre = append(dqre, &diskQuotaRegExp{
				pattern: re, limitSize: size})
		}
	}

	return dqre
}
