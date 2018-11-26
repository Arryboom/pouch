package containerplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/alibaba/pouch/apis/opts"
	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/utils"

	"github.com/magiconair/properties"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// PreUpdate defines plugin point where receives a container update request, in this plugin point user
// could change the container update body passed-in by http request body.
func (c *contPlugin) PreUpdate(in io.ReadCloser) (io.ReadCloser, error) {
	logrus.Infof("pre update method called")
	inputBuffer, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, err
	}
	logrus.Infof("update container with body %s", string(inputBuffer))

	type UpdateConfigInternal struct {
		types.Resources
		RestartPolicy types.RestartPolicy
		ImageID       string
		Env           []string
		Label         []string
		DiskQuota     string
		Network       string
	}

	var updateConfigInternal UpdateConfigInternal
	err = json.NewDecoder(bytes.NewReader(inputBuffer)).Decode(&updateConfigInternal)
	if err != nil {
		// if can't decode by UpdateConfigInternal,
		// we just pass it into pouch daemon to deal with it.
		logrus.Warn("failed to decode update body, pass it into pouch daemon")
		return ioutil.NopCloser(bytes.NewReader(inputBuffer)), nil
	}

	var diskQuota map[string]string
	if updateConfigInternal.DiskQuota != "" {
		// Notes: compatible with alidocker, if DiskQuota is not empty,
		// should also update the DiskQuota Label
		if !utils.StringInSlice(updateConfigInternal.Label, updateConfigInternal.DiskQuota) {
			updateConfigInternal.Label = append(updateConfigInternal.Label, updateConfigInternal.DiskQuota)
		}

		diskQuota, err = opts.ParseDiskQuota(strings.Split(updateConfigInternal.DiskQuota, ";"))
		if err != nil {
			logrus.Errorf("failed to parse update diskquota: %s, err: %v", updateConfigInternal.DiskQuota, err)
			return nil, errors.Wrapf(err, "failed to parse update diskquota(%s)", updateConfigInternal.DiskQuota)
		}
	}

	updateConfig := types.UpdateConfig{
		Resources:     updateConfigInternal.Resources,
		DiskQuota:     diskQuota,
		Env:           updateConfigInternal.Env,
		Label:         updateConfigInternal.Label,
		RestartPolicy: &updateConfigInternal.RestartPolicy,
	}

	// marshal it as stream and return to the caller
	var out bytes.Buffer
	err = json.NewEncoder(&out).Encode(updateConfig)
	logrus.Infof("after process update container body is %s", string(out.Bytes()))

	return ioutil.NopCloser(&out), err
}

// PostUpdate called after update method successful,
// the method accepts the rootfs path and envs of container.
// updates env file /etc/profile.d/dockernv.sh and /etc/instanceInfo
func (c *contPlugin) PostUpdate(rootfs string, env []string) error {
	logrus.Infof("post update method called")

	// if rootfs not exist, return
	if _, err := os.Stat(rootfs); err != nil {
		return nil
	}

	var (
		str              string
		propertiesEnvStr string
	)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 1 {
			parts = append(parts, "")
		}
		if len(parts[1]) > 0 && !strings.Contains(parts[0], ".") {
			s := strings.Replace(parts[1], "\\", "\\\\", -1)
			s = strings.Replace(s, "\"", "\\\"", -1)
			s = strings.Replace(s, "$", "\\$", -1)
			if parts[0] == "PATH" {
				// Note(cao.yin): refer to https://aone.alipay.com/project/532482/task/9745028
				s = parts[1] + ":$PATH"
			}
			str += fmt.Sprintf("export %s=\"%s\"\n", parts[0], s)
			propertiesEnvStr += fmt.Sprintf("env_%s=%s\n", parts[0], s)
		}
	}
	ioutil.WriteFile(filepath.Join(rootfs, "/etc/profile.d/pouchenv.sh"), []byte(str), 0755)

	properenv, err := os.Create(filepath.Join(rootfs, "/etc/instanceInfo"))
	if err != nil {
		return fmt.Errorf("Create env properties file faield: %v", err)
	}
	defer properenv.Close()

	p, err := properties.LoadString(propertiesEnvStr)
	if err != nil {
		return fmt.Errorf("Properties unable to load env string: %v", err)
	}
	_, err = p.Write(properenv, properties.ISO_8859_1)
	if err != nil {
		return fmt.Errorf("Unable to write container's env to properties file: %v", err)
	}

	return nil
}
