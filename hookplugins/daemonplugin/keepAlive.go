package daemonplugin

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/pkg/system"
)

var (
	homeDir        string
	pluginLock     sync.Mutex
	collectdClient = &http.Client{Timeout: 20 * time.Second}
)

func activePlugins() {
	activePluginsOnce()
	go cleanVmcommonDir()

	for range time.NewTicker(time.Second * 30).C {
		activePluginsOnce()
	}
}

func cleanVmcommonDir() {
	for range time.NewTicker(time.Hour * 23).C {
		files, err := ioutil.ReadDir(homeDir)
		if err != nil {
			log.With(nil).Errorf("read graph dir error. %s %v", homeDir, err)
			continue
		}
		existDir := make(map[string]struct{})
		for _, oneFile := range files {
			if !oneFile.IsDir() {
				continue
			}
			oneDir := filepath.Join(homeDir, oneFile.Name(), "top_foot_vm")
			if fi, ex := os.Stat(oneDir); ex == nil && fi.IsDir() {
				existDir[oneDir] = struct{}{}
			}
		}
		var ca []string
		var c types.ContainerJSON
		var afterWait bool
	checkIfExist:
		if len(existDir) > 0 {
			if afterWait {
				time.Sleep(time.Hour * 23)
			}
			ca, err = getAllContainers()
			if err != nil {
				log.With(nil).Errorf("get all containers error %v", err)
				continue
			}
			for _, id := range ca {
				c, err = getOneContainers(id)
				if err != nil {
					log.With(nil).Errorf("get one container error. %s %v", id, err)
					break
				}
				for _, oneMount := range c.Mounts {
					delete(existDir, oneMount.Source)
				}
			}
			if err != nil {
				continue
			}
			if afterWait {
				for oneDir := range existDir {
					log.With(nil).Infof("remove dir %s because it is useless", oneDir)
					os.RemoveAll(oneDir)
					if ba, err := ioutil.ReadDir(filepath.Dir(oneDir)); err == nil && len(ba) == 0 {
						log.With(nil).Infof("remove dir %s because it is useless", filepath.Dir(oneDir))
						os.RemoveAll(filepath.Dir(oneDir))
					}
				}
			} else {
				afterWait = true
				goto checkIfExist
			}
		}
	}
}

func activePluginsOnce() {
	pluginLock.Lock()
	defer pluginLock.Unlock()
	for _, one := range strings.Split(os.Getenv("EmbedPlugins"), ",") {
		if one == "" {
			continue
		}
		if one == "collectd" {
			resp, err := collectdClient.Get("http://127.0.0.1:5678/debug/version")
			if err == nil && resp.StatusCode == http.StatusOK {
				io.Copy(ioutil.Discard, resp.Body)
				resp.Body.Close()
				continue
			}
		} else {
			socketPath := fmt.Sprintf("/run/docker/plugins/%s/%s.sock", one, one)
			if _, ex := os.Stat(socketPath); ex == nil {
				c, err := net.Dial("unix", socketPath)
				if err == nil {
					c.Close()
					continue
				}
				os.Remove(socketPath)
			}
		}

		if f, e := os.OpenFile(fmt.Sprintf("/var/log/%s.log", one), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); e == nil {
			plugin := fmt.Sprintf("/opt/ali-iaas/pouch/plugins/%s", one)
			activeOne := osexec.Cmd{
				Path:   plugin,
				Args:   []string{plugin, system.GetNodeIP()},
				Stdout: f,
				Stderr: f,
			}
			if one == "aisnet" {
				activeOne.Args[1] = "-d"
			}
			if one == "nvidia-docker" {
				if _, err := os.Stat("/usr/bin/nvidia-modprobe"); err != nil {
					continue
				} else {
					activeOne.Args = []string{
						plugin,
						// because the sock of alinet is hardcode /run/docker/
						// nvidia-docker use /run/docker/ too.
						"-s", "/run/docker/plugins/nvidia-docker/"}
				}
			}
			if e = activeOne.Start(); e != nil {
				log.With(nil).Errorf("start plugins error %s, %v", one, e)
			} else {
				log.With(nil).Infof("start plugin success. %s", one)
				go func() {
					activeOne.Wait()
				}()
			}
			f.Close()
		}
	}
}
