package containerplugin

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/alibaba/pouch/pkg/log"
	"github.com/magiconair/properties"
)

var (
	// envHitList record the env key which will not set in /etc/profile.d/pouch.env
	envHitList = map[string]struct{}{
		"HOME": {},
		"USER": {},
	}
)

const enableEnvHistListKey = "pouch.EnableEnvHitList"

// PreUpdate defines plugin point where receives a container update request, in this plugin point user
// could change the container update body passed-in by http request body.
func (c *contPlugin) PreUpdate(ctx context.Context, in io.ReadCloser) (io.ReadCloser, error) {
	return in, nil
}

// PostUpdate called after update method successful,
// the method accepts the rootfs path and envs of container.
// updates env file /etc/profile.d/dockernv.sh and /etc/instanceInfo
func (c *contPlugin) PostUpdate(ctx context.Context, rootfs string, env []string) error {
	log.With(ctx).Infof("post update method called")

	// if rootfs not exist, return
	if _, err := os.Stat(rootfs); err != nil {
		return nil
	}

	enableEnvHitList := false

	v := getEnv(env, enableEnvHistListKey)
	if v == "true" {
		enableEnvHitList = true
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

		if enableEnvHitList {
			if _, exist := envHitList[parts[0]]; exist {
				continue
			}
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
