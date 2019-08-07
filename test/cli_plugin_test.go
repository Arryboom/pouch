package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/pouch/test/command"
	"github.com/alibaba/pouch/test/environment"
	"github.com/alibaba/pouch/test/util"

	"github.com/docker/docker/daemon/caps"
	"github.com/go-check/check"
	"github.com/gotestyourself/gotestyourself/icmd"
	"github.com/opencontainers/runtime-spec/specs-go"
)

const (
	copyPodHostsLabel = "pouch.CopyPodHosts"
)

// PouchPluginSuite is the test suite for ps CLI.
type PouchPluginSuite struct{}

func init() {
	check.Suite(&PouchPluginSuite{})
}

// SetUpSuite does common setup in the beginning of each test suite.
func (suite *PouchPluginSuite) SetUpSuite(c *check.C) {
	SkipIfFalse(c, environment.IsLinux)

	environment.PruneAllContainers(apiClient)
	PullImage(c, busyboxImage)
	PullImage(c, alios7u)
}

// TearDownTest does cleanup work in the end of each test.
func (suite *PouchPluginSuite) TearDownTest(c *check.C) {
}

func (suite *PouchPluginSuite) TestRunQuotaId(c *check.C) {
	if !environment.IsDiskQuota() {
		c.Skip("Host does not support disk quota")
	}
	name := "TestRunQuotaId"
	ID := "16777216"

	res := command.PouchRun("run", "-d", "--name", name, "-l", "DiskQuota=10G", "-l", "QuotaId="+ID, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	output := command.PouchRun("inspect", "-f", "{{.Config.Labels.QuotaId}}", name).Stdout()
	c.Assert(strings.TrimSpace(output), check.Equals, ID)

}

func (suite *PouchPluginSuite) TestRunAutoQuotaId(c *check.C) {
	if !environment.IsDiskQuota() {
		c.Skip("Host does not support disk quota")
	}
	name := "TestRunAutoQuotaId"
	AutoQuotaIDValue := "true"

	res := command.PouchRun("run", "-d", "--name", name, "-l", "DiskQuota=10G", "-l", "AutoQuotaId="+AutoQuotaIDValue, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	output := command.PouchRun("inspect", "-f", "{{.Config.Labels.AutoQuotaId}}", name).Stdout()
	c.Assert(strings.TrimSpace(output), check.Equals, AutoQuotaIDValue)
}

// TestRunDiskQuotaForAllDirsWithoutQuotaId: quota-id(<0) disk-quota:.*=10GB
func (suite *PouchPluginSuite) TestRunDiskQuotaForAllDirsWithoutQuotaId(c *check.C) {
	if !environment.IsDiskQuota() {
		c.Skip("Host does not support disk quota")
	}
	name := "TestRunDiskQuotaForAllDirsWithoutQuotaId"

	volumeName1 := "volume1"
	volumeName2 := "volume2"

	command.PouchRun("volume", "create", "-n", volumeName1, "-d", "local").Assert(c, icmd.Success)
	defer command.PouchRun("volume", "rm", volumeName1)

	command.PouchRun("volume", "create", "-n", volumeName2, "-d", "local", "-o", "opt.size=30M").Assert(c, icmd.Success)
	defer command.PouchRun("volume", "rm", volumeName2)

	command.PouchRun("run", "-d", `--disk-quota=".*=1g"`, "--quota-id=-1", "-v", volumeName1+":/mnt1", "-v", volumeName2+":/mnt2", "--name", name, busyboxImage, "top").Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-f", name)

	expct := icmd.Expected{
		ExitCode: 1,
		Err:      "Disk quota exceeded",
	}
	cmd := "dd if=/dev/zero of=/mnt2/test1 bs=1M count=20"
	command.PouchRun("exec", name, "sh", "-c", cmd).Assert(c, icmd.Success)

	cmd = "dd if=/dev/zero of=/mnt2/test2 bs=1M count=20"
	err := command.PouchRun("exec", name, "sh", "-c", cmd).Compare(expct)
	c.Assert(err, check.IsNil)

	expectedstring := "1048576"
	cmd = "df | grep mnt1"
	out := command.PouchRun("exec", name, "sh", "-c", cmd).Stdout()
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestDefaultNetworkMode: verify that default network mode is bridge
func (suite *PouchPluginSuite) TestDefaultNetworkMode(c *check.C) {
	name := "TestDefaultNetworkMode"
	res := command.PouchRun("run", "-d", "--name", name, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)
	networkmode, err := inspectFilter(name, ".HostConfig.NetworkMode")
	c.Assert(err, check.IsNil)
	c.Assert(networkmode, check.Equals, "bridge")
}

// TestUidFromIp: generate admin uid if env ali_admin_uid=0 exist
// uid=500 + ip with ending number, for example, 10.44.55.67 => uid 567
func (suite *PouchPluginSuite) TestUidFromIp(c *check.C) {
	name := "TestUidFromIp"
	endingnumber := 13 // uid=500+13=513
	FakeIP := "192.168.5." + strconv.Itoa(endingnumber)
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=vm", "-e", "RequestedIP="+FakeIP, "--env", "ali_admin_uid=0", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	//time.Sleep(30 * time.Second)

	expectedstring := strconv.Itoa(500 + endingnumber)
	cmd := "id -u admin"
	out := command.PouchRun("exec", "-i", name, "bash", "-c", cmd).Stdout()

	if strings.Compare(strings.Replace(out, "\n", "", -1), expectedstring) != 0 {
		c.Errorf("%s should be equal to %s", out, expectedstring)
	}
}

//TestSetUserToRootInRichContainer: set user to root if running in rich container mode
func (suite *PouchPluginSuite) TestSetUserToRootInRichContainer(c *check.C) {
	name := "TestSetUserToRootInRichContainer"
	res := command.PouchRun("run", "-d", "-u", "admin", "--env", "ali_run_mode=vm", "--env", "ali_admin_uid=0", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	//time.Sleep(60 * time.Second)
	expectedstring := "root"
	cmd := "ls -l /tmp/entry.log"
	out := ""

	i := 0
	for i < 20 {
		out = command.PouchRun("exec", "-i", name, "bash", "-c", cmd).Stdout()
		if !strings.Contains(out, expectedstring) {
			i++
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

	if i >= 20 {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestConvertDiskQuotaLabel: convert label DiskQuota to DiskQuota in ContainerConfig parameter
func (suite *PouchPluginSuite) TestConvertDiskQuotaLabel(c *check.C) {
	name := "TestConvertDiskQuotaLabel"
	res := command.PouchRun("run", "-d", "-l", "DiskQuota=\"/=1G\"", "--env", "ali_run_mode=vm", "--env", "ali_admin_uid=0", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "1G"
	output := command.PouchRun("inspect", "-f", "{{.Config.DiskQuota}}", name).Stdout()
	if !strings.Contains(output, expectedstring) {
		c.Errorf("%s should contains %s", output, expectedstring)
	}
}

//TestAliRunModeWithCommonVm: in rich container mode, change ali_run_mode=common_vm to ali_run_mode=vm
func (suite *PouchPluginSuite) TestAliRunModeWithCommonVm(c *check.C) {
	name := "TestAliRunModeWithCommonVm"
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--env", "ali_admin_uid=0", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "ali_run_mode=vm"
	output := command.PouchRun("inspect", "-f", "{{.Config.Env}}", name).Stdout()
	if !strings.Contains(output, expectedstring) {
		c.Errorf("%s should contains %s", output, expectedstring)
	}
}

//TestLabelsToEnv: in rich container mode, change ali_run_mode=common_vm to ali_run_mode=vm
func (suite *PouchPluginSuite) TestLabelsToEnv(c *check.C) {
	name := "TestLabelsToEnv"
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--env", "ali_admin_uid=0", "--env", "RequestedIP=192.168.5.11", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "ali_run_mode=\"vm\""
	cmd := "cat /etc/profile.d/pouchenv.sh"
	out := command.PouchRun("exec", name, "bash", "-c", cmd).Stdout()
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}

	expectedstring = "ali_admin_uid=\"511\""
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}

	expectedstring = "RequestedIP=\"192.168.5.11\""
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestCapabilitiesInRichContainer: in rich container mode, add some capabilities by default
// SYS_RESOURCE SYS_MODULE SYS_PTRACE SYS_PACCT NET_ADMIN SYS_ADMIN are added.
func (suite *PouchPluginSuite) TestCapabilitiesInRichContainer(c *check.C) {
	name := "TestCapabilitiesInRichContainer"
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--env", "ali_admin_uid=0", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstrings := [6]string{"SYS_RESOURCE", "SYS_MODULE", "SYS_PTRACE", "SYS_PACCT", "NET_ADMIN", "SYS_ADMIN"}
	output := command.PouchRun("inspect", "-f", "{{.HostConfig.CapAdd}}", name).Stdout()

	for _, value := range expectedstrings {
		if !strings.Contains(output, value) {
			c.Errorf("%s should contains %s", output, value)
		}
	}
}

// TestBindHostsHostnameResolvInRichContainer: in rich container mode, bind /etc/hosts /etc/hostname /etc/resolv.conf files into container
func (suite *PouchPluginSuite) TestBindHostsHostnameResolvInRichContainer(c *check.C) {
	name := "TestBindHostsHostnameResolvInRichContainer"
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--env", "ali_admin_uid=0", "-v", "/etc/:/tmp/etc/", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	cmd := "diff /etc/resolv.conf /tmp/etc/resolv.conf"
	command.PouchRun("exec", name, "bash", "-c", cmd).Assert(c, icmd.Success)

	//TODO: /etc/hostname /etc/hosts
}

// TestShmSizeIsHalfOfMemory: in rich container mode, set ShmSize to half of the limit of memory
func (suite *PouchPluginSuite) TestShmSizeIsHalfOfMemory(c *check.C) {
	name := "TestShmSizeIsHalfOfMemory"
	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--memory=8G", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)
	output := command.PouchRun("inspect", "-f", "{{.HostConfig.ShmSize}}", name).Stdout()
	c.Assert(strings.TrimSpace(output), check.Equals, "4294967296") // 4294967296=8x1024x1024x1024/2

	res = command.PouchRun("exec", name, "df", "-k", "/dev/shm")
	res.Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "4194304"), check.IsNil)
}

// TestSetHostnameEnv: set HOSTNAME env if HostName specified
func (suite *PouchPluginSuite) TestSetHostnameEnv(c *check.C) {
	name := "TestSetHostnameEnv"

	res := command.PouchRun("run", "-d", "--env", "ali_run_mode=common_vm", "--env", "HOSTNAME=myhello", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "HOSTNAME=\"myhello\""
	cmd := "cat /etc/profile.d/pouchenv.sh"
	out := command.PouchRun("exec", name, "bash", "-c", cmd).Stdout()
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestTrimPrefixContainerSlash: if VolumesFrom specified and the container name has a prefix of slash, trim it
func (suite *PouchPluginSuite) TestTrimPrefixContainerSlash(c *check.C) {
	con1name := "TestTrimPrefixContainerSlashcon1"
	con2name := "TestTrimPrefixContainerSlashcon2"

	vol1 := "myvol1"
	vol2 := "myvol2"
	command.PouchRun("volume", "create", "--name", vol1).Assert(c, icmd.Success)
	command.PouchRun("volume", "create", "--name", vol2).Assert(c, icmd.Success)
	defer command.PouchRun("volume", "remove", vol1)
	defer command.PouchRun("volume", "remove", vol2)

	con1id := command.PouchRun("run", "-d", "-v", vol1+":/v1", "-v", vol2+":/v2", "--name", con1name, alios7u).Stdout()
	defer DelContainerForceMultyTime(c, con1name)

	cmd := "echo hellopouch > /v1/tmpfile"
	command.PouchRun("exec", con1name, "bash", "-c", cmd).Assert(c, icmd.Success)
	command.PouchRun("stop", con1name).Assert(c, icmd.Success)

	command.PouchRun("run", "-d", "--volumes-from", "/"+con1id, "--name", con2name, alios7u).Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, con2name)
	cmd = "cat /v1/tmpfile"
	//time.Sleep(20 * time.Second)
	out := command.PouchRun("exec", con2name, "bash", "-c", cmd).Stdout()

	expectedstring := "hellopouch"
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestNetPriority: add net-priority into spec-annotations
func (suite *PouchPluginSuite) TestNetPriority(c *check.C) {
	name := "TestNetPriority"

	res := command.PouchRun("run", "-d", "--net-priority=7", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "net-priority:7"
	output := command.PouchRun("inspect", "-f", "{{.Config.SpecAnnotation}}", name).Stdout()
	if !strings.Contains(output, expectedstring) {
		c.Errorf("%s should contains %s", output, expectedstring)
	}

}

// TestDropCap: hook plugin will add more caps in create, test
// drop-cap include these caps also take effect
func (suite *PouchPluginSuite) TestDropCap(c *check.C) {
	name1 := "TestWithDefaultCap"
	name2 := "TestDropCap"

	// test default has NET_ADMIN capability
	command.PouchRun("run", "-d", "--name", name1, busyboxImage, "top").Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, name1)
	command.PouchRun("exec", name1, "brctl", "addbr", "foo").Assert(c, icmd.Success)

	// test extra added caps for rich container can be drop
	command.PouchRun("run", "-d", "--cap-drop", "NET_ADMIN", "--name", name2, busyboxImage, "top").Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, name2)

	expt := icmd.Expected{
		ExitCode: 1,
		Err:      "Operation not permitted",
	}
	err := command.PouchRun("exec", name2, "brctl", "addbr", "foo").Compare(expt)
	c.Assert(err, check.IsNil)

	defaultAddedCaps := [6]string{"SYS_RESOURCE", "SYS_MODULE", "SYS_PTRACE", "SYS_PACCT", "NET_ADMIN", "SYS_ADMIN"}
	output := command.PouchRun("inspect", "-f", "{{.HostConfig.CapAdd}}", name2).Stdout()

	for _, value := range defaultAddedCaps {
		if value == "NET_ADMIN" {
			if strings.Contains(output, value) {
				c.Errorf("NET_ADMIN should be droped")
			}
			continue
		}

		if !strings.Contains(output, value) {
			c.Errorf("%s should contains %s", output, value)
		}
	}
}

// TestPouchLabelConvert: Add label pouch.SupportCgroup=true to container. PreCreate hook
// will convert it to env pouchSupportCgroup=true
func (suite *PouchPluginSuite) TestPouchLabelConvert(c *check.C) {
	name := "TestLabelPouchSupportCgroup"
	res := command.PouchRun("run", "-d", "-l", "pouch.SupportCgroup=true", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	expectedstring := "pouchSupportCgroup=true"
	output := command.PouchRun("inspect", "-f", "{{.Config.Env}}", name).Stdout()
	c.Assert(util.PartialEqual(output, expectedstring), check.IsNil)

	// test cgroup is rw mount in container
	res = command.PouchRun("exec", name, "sh", "-c", "mkdir /sys/fs/cgroup/cpu/test")
	res.Assert(c, icmd.Success)
}

// TestEnvAliJvmCgroup: -e ali_jvm_cgroup=true allow container get all capabilities and make cgroup writeable
func (suite *PouchPluginSuite) TestEnvAliJvmCgroup(c *check.C) {
	name := "TestEnvAliJvmCgroup"
	res := command.PouchRun("run", "-d", "-e", "ali_jvm_cgroup=true", "--name", name, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	res = command.PouchRun("inspect", "-f", "{{.Config.Env}}", name)
	c.Assert(util.PartialEqual(res.Stdout(), "ali_jvm_cgroup"), check.IsNil)

	out := command.PouchRun("inspect", "-f", "{{.HostConfig.CapAdd}}", name).Stdout()
	allCaps := caps.GetAllCapabilities()
	for _, v := range allCaps {
		c.Assert(util.PartialEqual(out, strings.TrimPrefix(v, "CAP_")), check.IsNil)
	}

	// test cgroup writeable
	res = command.PouchRun("exec", name, "sh", "-c", "mkdir /sys/fs/cgroup/cpu/test")
	res.Assert(c, icmd.Success)
}

// isSetCopyPodHostsHook is to check container whether to set copyPodHostsPrestartHook
func isSetCopyPodHostsHook(rootDir string, cid string) (bool, error) {
	copyPodHostsHookPath := "/opt/ali-iaas/pouch/bin/prestart_hook_alipay"
	configFile := filepath.Join(rootDir, "containerd/state/io.containerd.runtime.v1.linux/default", cid, "config.json")
	f, err := os.Open(configFile)
	if err != nil {
		return false, err
	}

	spec := specs.Spec{}

	err = json.NewDecoder(f).Decode(&spec)
	if err != nil {
		return false, err
	}

	for _, h := range spec.Hooks.Prestart {
		if h.Path == copyPodHostsHookPath {
			return true, nil
		}
	}

	return false, nil
}

func checkMountExist(rootDir string, cid string, dest string) (bool, error) {
	info, err := apiClient.ContainerGet(context.Background(), cid)
	if err != nil {
		return false, err
	}

	for _, m := range info.Mounts {
		if m.Destination == dest {
			return true, nil
		}
	}

	return false, nil
}

func makeResolvConfFile(root string) (string, error) {
	fPath := filepath.Join(root, "resolv.conf")
	err := ioutil.WriteFile(fPath, []byte("nameserver 1.1.1.1"), 0644)
	return fPath, err
}

func makeHostnameFile(root string) (string, error) {
	fPath := filepath.Join(root, "hostname")
	err := ioutil.WriteFile(fPath, []byte("test.sqa.net"), 0644)
	return fPath, err
}

func (suite *PouchPluginSuite) TestNotSetCopyPodHosts(c *check.C) {
	rootDir, err := GetRootDir()
	c.Assert(err, check.IsNil)

	tmpDir, err := ioutil.TempDir("/tmp", "TestNotSetCopyPodHosts")
	c.Assert(err, check.IsNil)
	confFile, err := makeResolvConfFile(tmpDir)
	c.Assert(err, check.IsNil)

	defer os.RemoveAll(tmpDir)

	primaryName := "TestCWithPrimary"
	res := command.PouchRun("run", "-d", "--name", primaryName, alios7u)
	res.Assert(c, icmd.Success)
	primaryCid := strings.TrimSpace(res.Stdout())

	defer DelContainerForceMultyTime(c, primaryName)

	// case 1, vm mode and container network
	name1 := "TestCWithVMMode"
	res = command.PouchRun("run", "-d", "-e", "ali_run_mode=common_vm", "--name", name1,
		"--net", fmt.Sprintf("container:%s", primaryCid), "-v", fmt.Sprintf("%s:/etc/resolv.conf", confFile), alios7u)
	res.Assert(c, icmd.Success)
	cid := strings.TrimSpace(res.Stdout())
	// without label pouch.CopyPodHosts, it should not set CopyPodHostsPrestartHook
	setHook, err := isSetCopyPodHostsHook(rootDir, cid)
	c.Assert(err, check.IsNil)
	c.Assert(setHook, check.Equals, false)

	mountExist, err := checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, true)

	DelContainerForceMultyTime(c, name1)

	// case 2, with label pouch.CopyPodHosts, but not vm mode
	name2 := "TestCWithCopyPodNotVmMode"
	res = command.PouchRun("run", "-d", "-l", fmt.Sprintf("%s=true", copyPodHostsLabel),
		"--name", name2, "--net", fmt.Sprintf("container:%s", primaryCid), "-v", fmt.Sprintf("%s:/etc/resolv.conf", confFile), alios7u)
	res.Assert(c, icmd.Success)
	cid = strings.TrimSpace(res.Stdout())
	// without label pouch.CopyPodHosts, it should not set CopyPodHostsPrestartHook
	setHook, err = isSetCopyPodHostsHook(rootDir, cid)
	c.Assert(err, check.IsNil)
	c.Assert(setHook, check.Equals, false)

	mountExist, err = checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, true)

	DelContainerForceMultyTime(c, name2)
}

func (suite *PouchPluginSuite) TestSetCopyPodHosts(c *check.C) {
	rootDir, err := GetRootDir()
	c.Assert(err, check.IsNil)

	tmpDir, err := ioutil.TempDir("/tmp", "TestSetCopyPodHosts")
	c.Assert(err, check.IsNil)

	otherTmpDir, err := ioutil.TempDir("/tmp", "TestOtherCopyPodHosts")
	c.Assert(err, check.IsNil)

	confFile, err := makeResolvConfFile(tmpDir)
	c.Assert(err, check.IsNil)

	_, err = makeResolvConfFile(otherTmpDir)
	c.Assert(err, check.IsNil)

	defer os.RemoveAll(tmpDir)
	defer os.RemoveAll(otherTmpDir)

	primaryName := "TestCWithPrimary"
	res := command.PouchRun("run", "-d", "--name", primaryName, alios7u)
	res.Assert(c, icmd.Success)

	primaryCid := strings.TrimSpace(res.Stdout())

	defer DelContainerForceMultyTime(c, primaryName)

	// case1: create container with ali.host.dns=true and container network
	name1 := "TestCWithCpPodHostsLabel"
	res = command.PouchRun("run", "-d", "--name", name1, "-l", "ali.host.dns=true", "-l", fmt.Sprintf("%s=true", copyPodHostsLabel),
		"--net", fmt.Sprintf("container:%s", primaryCid), "-v", fmt.Sprintf("%s:/etc/resolv.conf", confFile), "-v", fmt.Sprintf("%s:/tmp/etc", otherTmpDir), alios7u)
	res.Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, name1)

	cid := strings.TrimSpace(res.Stdout())
	// with label pouch.CopyPodHosts, it should set CopyPodHostsPrestartHook
	setHook, err := isSetCopyPodHostsHook(rootDir, cid)
	c.Assert(err, check.IsNil)
	c.Assert(setHook, check.Equals, true)

	// bind should be removed
	mountExist, err := checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	cmd := "diff /tmp/etc/resolv.conf /etc/resolv.conf"
	command.PouchRun("exec", name1, "bash", "-c", cmd).Assert(c, icmd.Success)
	command.PouchRun("exec", name1, "bash", "-c", "")

	// update /etc/resolv.conf
	addNameServer := "nameserver 1.1.1.1"
	cmd1 := fmt.Sprintf("echo '%s' >> /etc/resolv.conf", addNameServer)
	command.PouchRun("exec", name1, "bash", "-c", cmd1).Assert(c, icmd.Success)
	// write new nameserver to host otherTmpdir
	hostcmd := fmt.Sprintf("echo '%s' >> %s", addNameServer, fmt.Sprintf("%s/resolv.conf", otherTmpDir))
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	// restart container and verify again
	command.PouchRun("restart", name1).Assert(c, icmd.Success)
	// bind should be removed
	mountExist, err = checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	cmd = "diff /tmp/etc/resolv.conf /etc/resolv.conf"
	command.PouchRun("exec", name1, "bash", "-c", cmd).Assert(c, icmd.Success)

	// case2: create container with com.alipay.acs.container.server_type=DOCKER_VM and bridge network, add bind of /etc/hostname
	hostnameFile, err := makeHostnameFile(tmpDir)
	c.Assert(err, check.IsNil)

	_, err = makeHostnameFile(otherTmpDir)
	c.Assert(err, check.IsNil)

	confFile, err = makeResolvConfFile(tmpDir)
	c.Assert(err, check.IsNil)

	_, err = makeResolvConfFile(otherTmpDir)
	c.Assert(err, check.IsNil)

	name2 := "TestCWithCpPodHostsLabelv2"
	res = command.PouchRun("run", "-d", "--name", name2, "-l", "com.alipay.acs.container.server_type=DOCKER_VM", "-l",
		fmt.Sprintf("%s=true", copyPodHostsLabel), "-v", fmt.Sprintf("%s:/etc/resolv.conf", confFile),
		"-v", fmt.Sprintf("%s:/etc/hostname", hostnameFile), "-v", fmt.Sprintf("%s:/tmp/etc", otherTmpDir), alios7u, "sleep", "10000")
	res.Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, name2)

	cid = strings.TrimSpace(res.Stdout())
	// with label pouch.CopyPodHosts, it should set CopyPodHostsPrestartHook
	setHook, err = isSetCopyPodHostsHook(rootDir, cid)
	c.Assert(err, check.IsNil)
	c.Assert(setHook, check.Equals, true)

	// bind should be removed
	mountExist, err = checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	mountExist, err = checkMountExist(rootDir, cid, "/etc/hostname")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	cmd = "diff /tmp/etc/resolv.conf /etc/resolv.conf"
	command.PouchRun("exec", name2, "bash", "-c", cmd).Assert(c, icmd.Success)
	command.PouchRun("exec", name2, "bash", "-c", "")

	cmd = "diff /tmp/etc/hostname /etc/hostname"
	command.PouchRun("exec", name2, "bash", "-c", cmd).Assert(c, icmd.Success)
	command.PouchRun("exec", name2, "bash", "-c", "")

	// update /etc/resolv.conf
	addNameServer = "nameserver 1.1.1.1"
	cmd1 = fmt.Sprintf("echo '%s' >> /etc/resolv.conf", addNameServer)
	command.PouchRun("exec", name2, "bash", "-c", cmd1).Assert(c, icmd.Success)
	// write new nameserver to host otherTmpdir
	hostcmd = fmt.Sprintf("echo '%s' >> %s", addNameServer, fmt.Sprintf("%s/resolv.conf", otherTmpDir))
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	// update /etc/hostname
	newHostname := "test2.sqa.net"
	cmd1 = fmt.Sprintf("echo '%s' > /etc/hostname", newHostname)
	command.PouchRun("exec", name2, "bash", "-c", cmd1).Assert(c, icmd.Success)
	// write new hostname to host otherTmpdir
	hostcmd = fmt.Sprintf("echo '%s' > %s", newHostname, fmt.Sprintf("%s/hostname", otherTmpDir))
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	// restart container and verify again
	command.PouchRun("restart", name2).Assert(c, icmd.Success)
	// bind should be removed
	mountExist, err = checkMountExist(rootDir, cid, "/etc/resolv.conf")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	mountExist, err = checkMountExist(rootDir, cid, "/etc/hostname")
	c.Assert(err, check.IsNil)
	c.Assert(mountExist, check.Equals, false)

	cmd = "diff /tmp/etc/resolv.conf /etc/resolv.conf"
	command.PouchRun("exec", name2, "bash", "-c", cmd).Assert(c, icmd.Success)

	cmd = "diff /tmp/etc/hostname /etc/hostname"
	command.PouchRun("exec", name2, "bash", "-c", cmd).Assert(c, icmd.Success)
}

// TestLabelDiskQuotaFull: set -l DiskQuota=10G
func (suite *PouchPluginSuite) TestLabelDiskQuotaFull(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaFull1",
			label: "-l DiskQuota=10G",
		},
		{
			cname: "TestLabelDiskQuotaFull2",
			label: "-l DiskQuota=10G -l AutoQuotaId=true",
		},
		{
			cname: "TestLabelDiskQuotaFull3",
			label: "-l DiskQuota=10G -l AutoQuotaId=true -l QuotaId=16777710",
		},
		{
			cname: "TestLabelDiskQuotaFull4",
			label: "-l DiskQuota=10G -l QuotaId=16777715",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		ret.Assert(c, icmd.Success)
		defer command.PouchRun("rm", "-vf", test.cname)

		// check df output
		output := ret.Stdout()
		templ := ""
		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"overlay",
				"/abc",
				"/def",
				"/ghi",
				"/jkl",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] != "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if templ == "" {
						templ = strings.Join(parts[1:3], " ")
					} else {
						if strings.Join(parts[1:3], " ") != templ {
							c.Fatalf("failed to set disk quota: %s", line)
						}
					}
				}
			}
		}
	}
}

// TestLabelDiskQuotaRootFS: set -l DiskQuota=/=10G
func (suite *PouchPluginSuite) TestLabelDiskQuotaRootFS(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaRootFS1",
			label: "-l DiskQuota=/=10G",
		},
		{
			cname: "TestLabelDiskQuotaRootFS2",
			label: "-l DiskQuota=/=10G -l AutoQuotaId=true",
		},
		{
			cname: "TestLabelDiskQuotaRootFS3",
			label: "-l DiskQuota=/=10G -l AutoQuotaId=true -l QuotaId=16777720",
		},
		{
			cname: "TestLabelDiskQuotaRootFS4",
			label: "-l DiskQuota=/=10G -l QuotaId=16777725",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		ret.Assert(c, icmd.Success)
		defer command.PouchRun("rm", "-vf", test.cname)

		// check df output
		output := ret.Stdout()

		templ := ""
		// check root fs
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "overlay") {
				parts := strings.Fields(line)
				if len(parts) != 6 {
					c.Fatalf("invalid format: %s", line)
				}

				if parts[1] != "10485760" {
					c.Fatalf("failed to set disk quota: %s", line)
				}

				templ = strings.Join(parts[1:3], " ")
				break
			}
		}

		if templ == "" {
			c.Fatalf("failed to get templates disk quota")
		}

		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"/abc",
				"/def",
				"/ghi",
				"/jkl",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] == "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if strings.Join(parts[1:3], " ") == templ {
						c.Fatalf("failed to set disk quota: %s", line)
					}
				}
			}
		}
	}
}

// TestLabelDiskQuotaMountPoint: set -l DiskQuota=/abc=10G
func (suite *PouchPluginSuite) TestLabelDiskQuotaMountPoint(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaMountPoint1",
			label: "-l DiskQuota=/abc=10G",
		},
		{
			cname: "TestLabelDiskQuotaMountPoint2",
			label: "-l DiskQuota=/abc=10G -l AutoQuotaId=true",
		},
		{
			cname: "TestLabelDiskQuotaMountPoint3",
			label: "-l DiskQuota=/abc=10G -l AutoQuotaId=true -l QuotaId=16777730",
		},
		{
			cname: "TestLabelDiskQuotaMountPoint4",
			label: "-l DiskQuota=/abc=10G -l QuotaId=16777735",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		ret.Assert(c, icmd.Success)
		defer command.PouchRun("rm", "-vf", test.cname)

		// check df output
		output := ret.Stdout()

		templ := ""
		// check mount point /abc
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "/abc") {
				parts := strings.Fields(line)
				if len(parts) != 6 {
					c.Fatalf("invalid format: %s", line)
				}

				if parts[1] != "10485760" {
					c.Fatalf("failed to set disk quota: %s", line)
				}

				templ = strings.Join(parts[1:3], " ")
				break
			}
		}

		if templ == "" {
			c.Fatalf("failed to get templates disk quota")
		}

		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"overlay",
				"/def",
				"/ghi",
				"/jkl",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] == "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if strings.Join(parts[1:3], " ") == templ {
						c.Fatalf("failed to set disk quota: %s", line)
					}
				}
			}
		}
	}
}

// TestLabelDiskQuotaAddOperate: set -l DiskQuota=/&/abc=10G
func (suite *PouchPluginSuite) TestLabelDiskQuotaAddOperate(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaAddOperate1",
			label: "-l DiskQuota=/&/abc=10G",
		},
		{
			cname: "TestLabelDiskQuotaAddOperate2",
			label: "-l DiskQuota=/&/abc=10G -l AutoQuotaId=true",
		},
		{
			cname: "TestLabelDiskQuotaAddOperate3",
			label: "-l DiskQuota=/&/abc=10G -l AutoQuotaId=true -l QuotaId=16777740",
		},
		{
			cname: "TestLabelDiskQuotaAddOperate4",
			label: "-l DiskQuota=/&/abc=10G -l QuotaId=16777745",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		ret.Assert(c, icmd.Success)
		defer command.PouchRun("rm", "-vf", test.cname)

		// check df output
		output := ret.Stdout()

		templ := ""
		// check same quota rootfs and /abc
		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"overlay",
				"/abc",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] != "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if templ == "" {
						templ = strings.Join(parts[1:3], " ")
					} else {
						if strings.Join(parts[1:3], " ") != templ {
							c.Fatalf("failed to set disk quota: %s", line)
						}
					}
				}
			}
		}

		if templ == "" {
			c.Fatalf("failed to get templates disk quota")
		}

		// check different mount point
		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"/def",
				"/ghi",
				"/jkl",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] == "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if strings.Join(parts[1:3], " ") == templ {
						c.Fatalf("failed to set disk quota: %s", line)
					}
				}
			}
		}
	}
}

// TestLabelDiskQuotaRegExp1: set -l DiskQuota=.*=10G
func (suite *PouchPluginSuite) TestLabelDiskQuotaRegExp1(c *check.C) {
	test := struct {
		cname string
		label string
	}{
		cname: "TestLabelDiskQuotaRegExp1",
		label: "-l DiskQuota=.*=10G",
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)

	cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
	ret := command.PouchRun(strings.Fields(cmd)...)
	ret.Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-vf", test.cname)

	// check df output
	output := ret.Stdout()

	// check different mount point
	for _, line := range strings.Split(output, "\n") {
		for _, mountpoint := range []string{
			"overlay",
			"/abc",
			"/def",
			"/ghi",
			"/jkl",
		} {
			if strings.Contains(line, mountpoint) {
				parts := strings.Fields(line)
				if len(parts) != 6 {
					c.Fatalf("invalid format: %s", line)
				}

				if parts[1] != "10485760" {
					c.Fatalf("failed to set disk quota: %s", line)
				}
			}
		}
	}

	// check QuotaID is nil
	ret = command.PouchRun("inspect", "-f", "{{.Config.QuotaID}}", test.cname)
	ret.Assert(c, icmd.Success)

	output = ret.Stdout()
	if strings.TrimSpace(output) != "" {
		c.Fatalf("get QuotaID(%s), expect nil", output)
	}
}

// TestLabelDiskQuotaRegExp2: set -l DiskQuota=.*=10G set -l QuotaId
func (suite *PouchPluginSuite) TestLabelDiskQuotaRegExp2(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaRegExp2",
			label: "-l DiskQuota=.*=10G -l AutoQuotaId=true",
		},
		{
			cname: "TestLabelDiskQuotaRegExp3",
			label: "-l DiskQuota=.*=10G -l AutoQuotaId=true -l QuotaId=16777750",
		},
		{
			cname: "TestLabelDiskQuotaRegExp4",
			label: "-l DiskQuota=.*=10G -l QuotaId=16777755",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		ret.Assert(c, icmd.Success)
		defer command.PouchRun("rm", "-vf", test.cname)

		// check df output
		output := ret.Stdout()

		templ := ""
		// check same quota rootfs and /abc
		for _, line := range strings.Split(output, "\n") {
			for _, mountpoint := range []string{
				"overlay",
				"/abc",
				"/def",
				"/ghi",
				"/jkl",
			} {
				if strings.Contains(line, mountpoint) {
					parts := strings.Fields(line)
					if len(parts) != 6 {
						c.Fatalf("invalid format: %s", line)
					}

					if parts[1] != "10485760" {
						c.Fatalf("failed to set disk quota: %s", line)
					}

					if templ == "" {
						templ = strings.Join(parts[1:3], " ")
					} else {
						if strings.Join(parts[1:3], " ") != templ {
							c.Fatalf("failed to set disk quota: %s", line)
						}
					}
				}
			}

			// check QuotaID isn't nil
			ret = command.PouchRun("inspect", "-f", "{{.Config.QuotaID}}", test.cname)
			ret.Assert(c, icmd.Success)

			output = ret.Stdout()
			if output == "" {
				c.Fatalf("get QuotaID(%s), expect nil", output)
			}
		}
	}
}

// TestLabelDiskQuotaMulti1: set -l DiskQuota=/abc=10G;.*=20G
func (suite *PouchPluginSuite) TestLabelDiskQuotaMulti1(c *check.C) {
	test := struct {
		cname string
		label string
	}{
		cname: "TestLabelDiskQuotaMulti1",
		label: "-l DiskQuota=/abc=10G;/def=11G;/ghi=12G;/jlk=13G;.*=20G",
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)

	cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
	ret := command.PouchRun(strings.Fields(cmd)...)
	ret.Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-vf", test.cname)

	// check df output
	output := ret.Stdout()

	// check different mount point
	for _, line := range strings.Split(output, "\n") {
		for _, check := range []struct {
			mp   string
			size string
		}{
			{mp: "overlay", size: "20971520"},
			{mp: "/abc", size: "10485760"},
			{mp: "/def", size: "11534336"},
			{mp: "/ghi", size: "12582912"},
			{mp: "/jlk", size: "13631488"},
		} {
			if strings.Contains(line, check.mp) {
				parts := strings.Fields(line)
				if len(parts) != 6 {
					c.Fatalf("invalid format: %s", line)
				}

				if parts[1] != check.size {
					c.Fatalf("failed to set disk quota: %s", line)
				}
			}
		}
	}
}

// TestLabelDiskQuotaMulti2: set -l DiskQuota=/&/abc=10G;.*=20G
func (suite *PouchPluginSuite) TestLabelDiskQuotaMulti2(c *check.C) {
	test := struct {
		cname string
		label string
	}{
		cname: "TestLabelDiskQuotaMulti2",
		label: "-l DiskQuota=/&/abc=10G;.*=20G",
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)

	cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
	ret := command.PouchRun(strings.Fields(cmd)...)
	ret.Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-vf", test.cname)

	// check df output
	output := ret.Stdout()

	// check different mount point
	for _, line := range strings.Split(output, "\n") {
		for _, check := range []struct {
			mp   string
			size string
		}{
			{mp: "overlay", size: "10485760"},
			{mp: "/abc", size: "10485760"},
			{mp: "/def", size: "20971520"},
			{mp: "/ghi", size: "20971520"},
			{mp: "/jlk", size: "20971520"},
		} {
			if strings.Contains(line, check.mp) {
				parts := strings.Fields(line)
				if len(parts) != 6 {
					c.Fatalf("invalid format: %s", line)
				}

				if parts[1] != check.size {
					c.Fatalf("failed to set disk quota: %s", line)
				}
			}
		}
	}
}

// TestLabelDiskQuotaInvalid: set invalid disk quota
func (suite *PouchPluginSuite) TestLabelDiskQuotaInvalid(c *check.C) {
	tests := []struct {
		cname string
		label string
	}{
		{
			cname: "TestLabelDiskQuotaInvalid2",
			label: "-l DiskQuota=/&/abc&.*=10G",
		},
		{
			cname: "TestLabelDiskQuotaInvalid4",
			label: "-l DiskQuota=/&/abc&.*=10G;.*=20G",
		},
	}

	commonCmd := fmt.Sprintf("-v /abc -v /def -v /ghi -v /jlk %s df", busyboxImage)
	for _, test := range tests {
		cmd := fmt.Sprintf("run --name %s %s %s", test.cname, test.label, commonCmd)
		ret := command.PouchRun(strings.Fields(cmd)...)
		defer command.PouchRun("rm", "-vf", test.cname)
		if ret.Error == nil {
			c.Fatalf("failed to check invalid disk quota")
		}
	}
}

// TestCpusharePreloadHook tests cpushare preload prestart hook
func (suite *PouchPluginSuite) TestCpusharePreloadHook(c *check.C) {
	cname := "TestCpusharePreloadHook"
	command.PouchRun("run", "-d", "--net=none", "-e", "ALIPAY_SIGMA_CPUMODE=cpushare", "--name", cname, alios7u).Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, cname)

	output := command.PouchRun("exec", cname, "cat", "/etc/ld.so.preload").Stdout()
	expectedString := "/lib/libsysconf-alipay.so"
	if !strings.Contains(output, expectedString) {
		c.Errorf("%s should containers %s", output, expectedString)
	}
}

// TestCopyHosts tests label ali.host.dns=true, com.alipay.acs.container.server.type=DOCKER_VM and env CopyHosts=true
func (suite *PouchPluginSuite) TestCopyHosts(c *check.C) {
	tmpDir, err := ioutil.TempDir("/tmp", "TestCopyHosts")
	c.Assert(err, check.IsNil)
	defer os.RemoveAll(tmpDir)

	//case 1: add label ali.host.dns=true
	name1 := "TestCopyHostsv1"
	hostcmd := fmt.Sprintf("cp /etc/hosts %s/hosts", tmpDir)
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	res := command.PouchRun("run", "-d", "-l", "ali.host.dns=true", "-v", "/etc/:/tmp/etc/", "-v", fmt.Sprintf("%s:/tmp/CopyHosts", tmpDir), "--name", name1, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name1)
	res.Assert(c, icmd.Success)

	output := command.PouchRun("exec", name1, "sh", "-c", "cat /proc/1/cmdline").Stdout()
	if !strings.HasPrefix(output, "top") {
		c.Errorf("%s should be %s", output, "top")
	}

	diffCmd := "diff /etc/hosts /tmp/etc/hosts"
	command.PouchRun("exec", name1, "sh", "-c", diffCmd).Assert(c, icmd.Success)
	expectedstring := "label__ali_host_dns=true"
	output = command.PouchRun("inspect", "-f", "{{.Config.Env}}", name1).Stdout()
	if !strings.Contains(output, expectedstring) {
		c.Errorf("%s should contains %s", output, expectedstring)
	}

	addHosts := "1.1.1.1 a.a.a.a"
	cmdSetHosts := fmt.Sprintf("echo '%s' >> /etc/hosts", addHosts)
	command.PouchRun("exec", name1, "sh", "-c", cmdSetHosts).Assert(c, icmd.Success)
	// write new hosts to host tmpDir
	hostcmd = fmt.Sprintf("echo '%s' >> %s", addHosts, fmt.Sprintf("%s/hosts", tmpDir))
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	command.PouchRun("restart", name1).Assert(c, icmd.Success)
	diffCmd = "diff /tmp/CopyHosts/hosts /etc/hosts"
	command.PouchRun("exec", name1, "sh", "-c", diffCmd).Assert(c, icmd.Success)

	// case 2 : add label com.alipay.acs.container.server_type=DOCKER_VM
	name2 := "TestCopyHostsv2"
	res = command.PouchRun("run", "-d", "-l", "com.alipay.acs.container.server_type=DOCKER_VM", "-v", "/etc/:/tmp/etc/", "-v", fmt.Sprintf("%s:/tmp/CopyHosts", tmpDir), "--name", name2, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name2)
	res.Assert(c, icmd.Success)

	output = command.PouchRun("exec", name1, "sh", "-c", "cat /proc/1/cmdline").Stdout()
	if !strings.HasPrefix(output, "top") {
		c.Errorf("%s should be %s", output, "top")
	}

	diffCmd = "diff /etc/resolv.conf /tmp/etc/resolv.conf"
	command.PouchRun("exec", name2, "sh", "-c", diffCmd).Assert(c, icmd.Success)
	expectedstring = "label__com_alipay_acs_container_server_type=DOCKER_VM"
	output = command.PouchRun("inspect", "-f", "{{.Config.Env}}", name2).Stdout()
	if !strings.Contains(output, expectedstring) {
		c.Errorf("%s should contains %s", output, expectedstring)
	}

	hostcmd = fmt.Sprintf("cp /etc/resolv.conf %s/resolv.conf", tmpDir)
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	// update /etc/resolv.conf
	addNameServer := "nameserver 1.1.1.1"
	cmdSetResolv := fmt.Sprintf("echo '%s' >> /etc/resolv.conf", addNameServer)
	command.PouchRun("exec", name2, "sh", "-c", cmdSetResolv).Assert(c, icmd.Success)
	// write new nameserver to host otherTmpdir
	hostcmd = fmt.Sprintf("echo '%s' >> %s", addNameServer, fmt.Sprintf("%s/resolv.conf", tmpDir))
	icmd.RunCommand("bash", "-c", hostcmd).Assert(c, icmd.Success)

	command.PouchRun("restart", name2).Assert(c, icmd.Success)
	diffCmd = "diff /tmp/CopyHosts/resolv.conf /etc/resolv.conf"
	command.PouchRun("exec", name2, "sh", "-c", diffCmd).Assert(c, icmd.Success)
}

func verifyEnvFileNotIncludeKey(c *check.C, output string, disableKeys []string) {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		env := strings.SplitN(parts[1], "=", 2)
		if len(env) != 2 {
			continue
		}

		for _, key := range disableKeys {
			c.Assert(env[0], check.Not(check.Equals), key)
		}
	}
}

// TestContainerWithEnableEnvHitList tests for enable env hit list
func (suite *PouchPluginSuite) TestContainerWithEnableEnvHitList(c *check.C) {
	cname := "TestContainerWithEnableEnvHitList"
	disableKey := []string{"HOME", "USER"}

	command.PouchRun("run", "-d", "--net=none", "-e", "pouch.EnableEnvHitList=true", "-e", "HOME=/root", "--name", cname, alios7u).Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, cname)

	output := command.PouchRun("exec", cname, "cat", "/etc/profile.d/pouchenv.sh").Stdout()
	verifyEnvFileNotIncludeKey(c, output, disableKey)

	// update env
	command.PouchRun("update", "-e", "USER=root", cname).Assert(c, icmd.Success)
	output = command.PouchRun("exec", cname, "cat", "/etc/profile.d/pouchenv.sh").Stdout()
	verifyEnvFileNotIncludeKey(c, output, disableKey)

	command.PouchRun("restart", cname).Assert(c, icmd.Success)
	output = command.PouchRun("exec", cname, "cat", "/etc/profile.d/pouchenv.sh").Stdout()
	verifyEnvFileNotIncludeKey(c, output, disableKey)
}

// TestEnvSN tests check env SN whether have been set into /dev/mem.
func (suite *PouchPluginSuite) TestEnvSN(c *check.C) {
	cname := "TestEnvSN"
	sn := "abcdefg"

	command.PouchRun("run", "-d",
		"--name", cname,
		"-e", "SN="+sn,
		"-v", "/sbin/dmidecode:/tmp/dmidecode:ro",
		alios7u, "sleep", "10000").Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, cname)

	res := command.PouchRun("exec", cname, "/tmp/dmidecode", "-s", "system-serial-number")
	res.Assert(c, icmd.Success)

	stdout := res.Stdout()
	if !strings.Contains(stdout, sn) {
		c.Fatalf("failed to get sn in container, got(%s), want(%s)", stdout, sn)
	}
}

// TestAddEnvironment add pouch environment pouch_container_image and pouch_container_id
func (suite *PouchPluginSuite) TestAddEnvironment(c *check.C) {
	cname := "TestAddEnvironment"

	command.PouchRun("run", "-d",
		"--name", cname,
		alios7u, "sleep", "10000").Assert(c, icmd.Success)
	defer DelContainerForceMultyTime(c, cname)

	res := command.PouchRun("inspect", "-f", "{{.Config.Env}}", cname)
	expect := "pouch_container_image=" + alios7u
	output := res.Stdout()
	c.Assert(util.PartialEqual(output, expect), check.IsNil)

	res = command.PouchRun("inspect", "-f", "{{.ID}}", cname)
	id := res.Stdout()
	id = strings.TrimSpace(id)
	expect = "pouch_container_id=" + id
	c.Assert(util.PartialEqual(output, expect), check.IsNil)

	expectedstring := "pouch_container_image=\"" + alios7u + "\""
	cmd := "cat /etc/profile.d/pouchenv.sh"
	out := command.PouchRun("exec", cname, "bash", "-c", cmd).Stdout()
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
	expectedstring = "pouch_container_id=\"" + id + "\""
	if !strings.Contains(out, expectedstring) {
		c.Errorf("%s should contains %s", out, expectedstring)
	}
}

// TestRunWithRequestedIP is to verify run container with ipv4 address by env RequestedIP
func (suite *PouchPluginSuite) TestRunWithRequestedIP(c *check.C) {
	cname1 := "TestRunWithRequestedIP"
	ip := "192.168.5.100"

	command.PouchRun("run", "-d", "--name", cname1, "-e", fmt.Sprintf("RequestedIP=%s", ip), alios7u, "sleep", "1000").Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-vf", cname1)

	res := command.PouchRun("exec", cname1, "ip", "addr", "show")
	res.Assert(c, icmd.Success)

	stdout := res.Stdout()
	found := false
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, ip) {
			found = true
			break
		}
	}

	c.Assert(found, check.Equals, true)

	cname2 := "TestRunWithIPCheckEnv"
	ip = "192.168.5.101"
	command.PouchRun("run", "-d", "--name", cname2, "--ip", ip, alios7u, "sleep", "1000").Assert(c, icmd.Success)
	defer command.PouchRun("rm", "-vf", cname2)

	res = command.PouchRun("exec", cname2, "cat", "/etc/profile.d/pouchenv.sh")
	res.Assert(c, icmd.Success)

	stdout = res.Stdout()
	found = false
	expectEnv := fmt.Sprintf(`RequestedIP="%s"`, ip)
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, expectEnv) {
			found = true
			break
		}
	}

	c.Assert(found, check.Equals, true)
}

// TestNoMountCgroup: -e no_mount_cgroup=true allow container not mount cgroup
// but if init process is systemd, it won't take effect
func (suite *PouchPluginSuite) TestNoMountCgroup(c *check.C) {
	name := "TestNoMountCgroup"
	res := command.PouchRun("run", "-d", "-e", "no_mount_cgroup=true", "--name", name, busyboxImage, "top")
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	res = command.PouchRun("exec", name, "sh", "-c", "ls /sys/fs/cgroup | wc -l").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "0\n"), check.IsNil)

	res = command.PouchRun("exec", name, "sh", "-c", "cat /proc/mounts | grep cgroup | wc -l").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "0\n"), check.IsNil)
}

// TestHostname test container get correct hostname
func (suite *PouchPluginSuite) TestHostname(c *check.C) {
	for _, t := range []struct {
		name string
		env  string
	}{
		{
			name: "TestHostnameNormalMode-1",
			env:  "TEST_HOSTNAME=test",
		},
		{
			name: "TestHostnameNormalMode-2",
			env:  "TEST HOSTNAME=test",
		},
	} {
		res := command.PouchRun("run", "-d", "-e", t.env, "--hostname", "IAmHostname", "--name", t.name, alios7u)
		defer DelContainerForceMultyTime(c, t.name)
		res.Assert(c, icmd.Success)

		res = command.PouchRun("exec", t.name, "hostname").Assert(c, icmd.Success)
		c.Assert(util.PartialEqual(res.Stdout(), "IAmHostname"), check.IsNil)
	}

	for _, t := range []struct {
		name string
		env  string
	}{
		{
			name: "TestHostnameVMMode-1",
			env:  "TEST_HOSTNAME=test",
		},
	} {
		res := command.PouchRun("run", "-d", "-e", t.env, "-e", "ali_run_mode=vm", "--hostname", "IAmHostname", "--name", t.name, alios7u)
		defer DelContainerForceMultyTime(c, t.name)
		res.Assert(c, icmd.Success)

		res = command.PouchRun("exec", t.name, "hostname").Assert(c, icmd.Success)
		c.Assert(util.PartialEqual(res.Stdout(), "IAmHostname"), check.IsNil)
	}
}

// TestMtab tests /etc/mtab should create every container start, we use /dev/hugepages/ mount
// systemd will mount /dev/hugepages if kernel support, actually we support here, so we test
// run/start/restart, every case should have /dev/hugepages exist and mount tab in /etc/mtab
func (suite *PouchPluginSuite) TestMtab(c *check.C) {
	name := "TestMtab"
	res := command.PouchRun("run", "-d", "-e", "ali_run_mode=vm", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	command.PouchRun("exec", name, "ls", "/dev/hugepages").Assert(c, icmd.Success)
	res = command.PouchRun("exec", name, "cat", "/etc/mtab").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "hugepages"), check.IsNil)

	// stop and start container
	command.PouchRun("stop", name).Assert(c, icmd.Success)
	command.PouchRun("start", name).Assert(c, icmd.Success)

	command.PouchRun("exec", name, "ls", "/dev/hugepages").Assert(c, icmd.Success)
	res = command.PouchRun("exec", name, "cat", "/etc/mtab").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "hugepages"), check.IsNil)

	// restart container
	command.PouchRun("restart", name).Assert(c, icmd.Success)

	command.PouchRun("exec", name, "ls", "/dev/hugepages").Assert(c, icmd.Success)
	res = command.PouchRun("exec", name, "cat", "/etc/mtab").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "hugepages"), check.IsNil)
}

// TestInitWithAdminUid test -e ali_admin_uid passed, init process use admin
// user should run with this uid
func (suite *PouchPluginSuite) TestInitWithAdminUid(c *check.C) {
	name := "TestInitWithAdminUid"
	res := command.PouchRun("run", "-d", "-u", "admin", "-e", "ali_admin_uid=555", "--name", name, alios7u)
	defer DelContainerForceMultyTime(c, name)
	res.Assert(c, icmd.Success)

	res = command.PouchRun("exec", name, "sh", "-c", "cat /proc/1/status | grep Uid").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "555\n"), check.IsNil)

	res = command.PouchRun("exec", name, "sh", "-c", "cat /proc/1/status | grep Gid").Assert(c, icmd.Success)
	c.Assert(util.PartialEqual(res.Stdout(), "555\n"), check.IsNil)
}
