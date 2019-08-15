package daemonplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/log"
)

var (
	pouchClient = http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/pouchd.sock")
			},
		},
		Timeout: time.Second * 30,
	}
)

func getAllContainers() (ca []string, err error) {
	resp, err := pouchClient.Get("http://127.0.0.1/containers/json?all=true")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var respArr []types.Container
	if err = json.NewDecoder(resp.Body).Decode(&respArr); err != nil {
		return
	}
	for _, one := range respArr {
		ca = append(ca, one.ID)
	}
	return
}

func getOneContainers(idOrName string) (c types.ContainerJSON, err error) {
	resp, err := pouchClient.Get(fmt.Sprintf("http://127.0.0.1/containers/%s/json", idOrName))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return
	}
	return
}

func setupEnv() {
	if b, err := ioutil.ReadFile("/etc/sysconfig/pouch"); err != nil {
		log.With(nil).Infof("read config file error %v", err)
	} else {
		for _, line := range bytes.Split(b, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if bytes.Contains(line, []byte("--set-env")) && !bytes.HasPrefix(line, []byte("#")) {
				splitByComma := bytes.Contains(line, []byte("--set-env-comma"))
				splitChar := byte(' ')
				var index int
				if splitByComma {
					index = bytes.Index(line, []byte("--set-env-comma"))
					if index != -1 {
						index += len("--set-env-comma")
					}
				} else {
					index = bytes.Index(line, []byte("--set-env"))
					if index != -1 {
						index += len("--set-env")
					}
				}
				if index < len(line) {
					splitChar = line[index]
				}

				arr := bytes.SplitN(line, []byte{splitChar}, 2)
				if len(arr) < 2 {
					continue
				}
				val := arr[1]
				var kv [][]byte
				if splitByComma {
					kv = bytes.Split(val, []byte{','})
				} else {
					kv = bytes.Split(val, []byte{':'})
				}
				for _, oneKv := range kv {
					tmpArr := bytes.SplitN(oneKv, []byte{'='}, 2)
					if len(tmpArr) == 2 {
						os.Setenv(string(bytes.TrimSpace(tmpArr[0])), string(bytes.TrimSpace(tmpArr[1])))
					} else {
						os.Setenv(string(bytes.TrimSpace(tmpArr[0])), "")
					}
				}
			}
		}
	}
}
