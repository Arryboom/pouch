package daemonplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/alibaba/pouch/hookplugins"
	"github.com/alibaba/pouch/pkg/log"
)

type daemonPlugin struct{}

func init() {
	hookplugins.RegisterDaemonPlugin(&daemonPlugin{})
}

// PreStartHook is invoked by pouch daemon before real start, in this hook user could start http proxy or other
// standalone process plugins
// copy config from /etc/pouch/config.json to /etc/sysconfig/pouch
// and start plugin processes than daemon depended
func (d *daemonPlugin) PreStartHook() error {
	log.With(nil).Infof("pre-start hook in daemon is called")
	configMap := make(map[string]interface{}, 8)
	if _, ex := os.Stat("/etc/pouch/config.json"); ex == nil {
		f, err := os.OpenFile("/etc/pouch/config.json", os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		err = json.NewDecoder(f).Decode(&configMap)
		f.Close()
		if err != nil {
			return err
		}
		if s, ok := configMap["home-dir"].(string); ok {
			homeDir = s
		}
	}
	setupEnv()
	b, e := exec.Command("/opt/ali-iaas/pouch/bin/daemon_prestart.sh", homeDir).CombinedOutput()
	if e != nil {
		return fmt.Errorf("daemon prestart execute error. %s %v", string(b), e)
	}
	log.With(nil).Infof("daemon_prestart output %s", string(b))

	go activePlugins()
	return nil
}

// PreStopHook stops plugin processes than start ed by PreStartHook.
func (d *daemonPlugin) PreStopHook() error {
	log.With(nil).Infof("pre-stop hook in daemon is called")
	b, e := exec.Command("/opt/ali-iaas/pouch/bin/daemon_prestop.sh").CombinedOutput()
	if e != nil {
		return e
	}
	log.With(nil).Infof("daemon_prestop output %s", string(b))
	return nil
}
