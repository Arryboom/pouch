package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/daemon/config"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// daemonUpdateDescription is used to describe updatedaemon command in detail and auto generate command doc.
var daemonUpdateDescription = "Update daemon's configurations, if daemon is stoped, it will just update config file. " +
	"Online update just including: image proxy, label, offline update including: manager white list, debug level, " +
	"execute root directory, bridge name, bridge IP, fixed CIDR, defaut gateway, iptables, ipforwark, userland proxy. " +
	"If pouchd is alive, you can only use --offline=true to update config file"

// DaemonUpdateCommand use to implement 'updatedaemon' command, it modifies the configurations of a container.
type DaemonUpdateCommand struct {
	baseCommand

	configFile string
	offline    bool

	debug            bool
	imageProxy       string
	label            []string
	managerWhiteList string
	execRoot         string
	disableBridge    bool
	bridgeName       string
	bridgeIP         string
	fixedCIDRv4      string
	defaultGatewayv4 string
	iptables         bool
	ipforward        bool
	userlandProxy    bool
	logMaxFile       string
	logMaxSize       string

	homeDir     string
	snapshotter string

	defaultLogType string
	logEnv         string
	syslogAddress  string
	syslogFacility string
	syslogFormat   string
	logTag         string

	allowMultiSnapshotter bool
	proxyPlugin           string
	proxyPluginAddress    string
	proxyPluginType       string
}

// Init initialize updatedaemon command.
func (udc *DaemonUpdateCommand) Init(c *Cli) {
	udc.cli = c
	udc.cmd = &cobra.Command{
		Use:   "updatedaemon [OPTIONS]",
		Short: "Update the configurations of pouchd",
		Long:  daemonUpdateDescription,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return udc.daemonUpdateRun(args)
		},
		Example: daemonUpdateExample(),
	}
	udc.addFlags()
}

// addFlags adds flags for specific command.
func (udc *DaemonUpdateCommand) addFlags() {
	flagSet := udc.cmd.Flags()
	flagSet.SetInterspersed(false)
	flagSet.StringVar(&udc.configFile, "config-file", "/etc/pouch/config.json", "specified config file for updating daemon")
	flagSet.BoolVar(&udc.offline, "offline", false, "just update daemon config file")

	flagSet.BoolVar(&udc.debug, "debug", false, "update daemon debug mode")
	flagSet.StringVar(&udc.imageProxy, "image-proxy", "", "update daemon image proxy")
	flagSet.StringVar(&udc.managerWhiteList, "manager-white-list", "", "update daemon manager white list")
	flagSet.StringSliceVar(&udc.label, "label", nil, "update daemon labels")
	flagSet.StringVar(&udc.execRoot, "exec-root-dir", "", "update exec root directory for network")
	flagSet.BoolVar(&udc.disableBridge, "disable-bridge", false, "disable bridge network")
	flagSet.StringVar(&udc.bridgeName, "bridge-name", "", "update daemon bridge device")
	flagSet.StringVar(&udc.bridgeIP, "bip", "", "update daemon bridge IP")
	flagSet.StringVar(&udc.fixedCIDRv4, "fixed-cidr", "", "update daemon bridge fixed CIDR")
	flagSet.StringVar(&udc.defaultGatewayv4, "default-gateway", "", "update daemon bridge default gateway")
	flagSet.BoolVar(&udc.iptables, "iptables", true, "update daemon with iptables")
	flagSet.BoolVar(&udc.ipforward, "ipforward", true, "udpate daemon with ipforward")
	flagSet.BoolVar(&udc.userlandProxy, "userland-proxy", false, "update daemon with userland proxy")
	flagSet.StringVar(&udc.logMaxFile, "log-opt-max-file", "", "update daemon max-file configured in default-log-config.Config")
	flagSet.StringVar(&udc.logMaxSize, "log-opt-max-size", "", "update daemon max-size configured in default-log-config.Config")
	flagSet.StringVar(&udc.homeDir, "home-dir", "", "update daemon home dir")
	flagSet.StringVar(&udc.snapshotter, "snapshotter", "", "update daemon snapshotter")
	flagSet.StringVar(&udc.defaultLogType, "default-log-type", "", "update default log type")
	flagSet.StringVar(&udc.logEnv, "log-env", "", "update log driver env")
	flagSet.StringVar(&udc.syslogAddress, "syslog-address", "", "update syslog log driver address")
	flagSet.StringVar(&udc.syslogFacility, "syslog-facility", "", "update syslog log driver facility")
	flagSet.StringVar(&udc.syslogFormat, "syslog-format", "", "update syslog log driver format")
	flagSet.StringVar(&udc.logTag, "log-tag", "", "update log driver tag")
	flagSet.BoolVar(&udc.allowMultiSnapshotter, "allow-multi-snapshotter", false, "update daemon allow-multi-snapshotter")
	flagSet.StringVar(&udc.proxyPlugin, "proxy-plugin", "", "update daemon proxy-plugin")
	flagSet.StringVar(&udc.proxyPluginAddress, "proxy-plugin-address", "", "update daemon proxy-plugin's address")
	flagSet.StringVar(&udc.proxyPluginType, "proxy-plugin-type", "", "update daemon proxy-plugin's type")
}

// daemonUpdateRun is the entry of updatedaemon command.
func (udc *DaemonUpdateCommand) daemonUpdateRun(args []string) error {
	ctx := context.Background()

	apiClient := udc.cli.Client()

	msg, err := apiClient.SystemPing(ctx)
	if !udc.offline && err == nil && msg == "OK" {
		// TODO: daemon support more configures for update online, such as debug level.
		daemonConfig := &types.DaemonUpdateConfig{
			ImageProxy: udc.imageProxy,
			Labels:     udc.label,
		}

		err = apiClient.DaemonUpdate(ctx, daemonConfig)
		if err != nil {
			return errors.Wrap(err, "failed to update alive daemon config")
		}
	} else {
		// offline update config file.
		err = udc.updateDaemonConfigFile()
		if err != nil {
			return errors.Wrap(err, "failed to update daemon config file.")
		}
	}

	return nil
}

// updateDaemonConfigFile is just used to update config file.
func (udc *DaemonUpdateCommand) updateDaemonConfigFile() error {
	// read config from file.
	contents, err := ioutil.ReadFile(udc.configFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read config file(%s)", udc.configFile)
	}

	daemonConfig := &config.Config{}
	// do not return error if config file is empty
	if err := json.NewDecoder(bytes.NewReader(contents)).Decode(daemonConfig); err != nil && err != io.EOF {
		return errors.Wrapf(err, "failed to decode json: %s", udc.configFile)
	}

	flagSet := udc.cmd.Flags()

	if flagSet.Changed("image-proxy") {
		daemonConfig.ImageProxy = udc.imageProxy
	}

	if flagSet.Changed("manager-white-list") {
		daemonConfig.TLS.ManagerWhiteList = udc.managerWhiteList
	}

	// TODO: add parse labels

	if flagSet.Changed("exec-root-dir") {
		daemonConfig.NetworkConfig.ExecRoot = udc.execRoot
	}

	if flagSet.Changed("disable-bridge") {
		daemonConfig.NetworkConfig.BridgeConfig.DisableBridge = udc.disableBridge
	}

	if flagSet.Changed("bridge-name") {
		daemonConfig.NetworkConfig.BridgeConfig.Name = udc.bridgeName
	}

	if flagSet.Changed("bip") {
		daemonConfig.NetworkConfig.BridgeConfig.IPv4 = udc.bridgeIP
	}

	if flagSet.Changed("fixed-cidr") {
		daemonConfig.NetworkConfig.BridgeConfig.FixedCIDRv4 = udc.fixedCIDRv4
	}

	if flagSet.Changed("default-gateway") {
		daemonConfig.NetworkConfig.BridgeConfig.GatewayIPv4 = udc.defaultGatewayv4
	}

	if flagSet.Changed("iptables") {
		daemonConfig.NetworkConfig.BridgeConfig.IPTables = udc.iptables
	}

	if flagSet.Changed("ipforward") {
		daemonConfig.NetworkConfig.BridgeConfig.IPForward = udc.ipforward
	}

	if flagSet.Changed("userland-proxy") {
		daemonConfig.NetworkConfig.BridgeConfig.UserlandProxy = udc.userlandProxy
	}

	if flagSet.Changed("log-opt-max-file") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["max-file"] = udc.logMaxFile
	}

	if flagSet.Changed("log-opt-max-size") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["max-size"] = udc.logMaxSize
	}

	if flagSet.Changed("home-dir") {
		daemonConfig.HomeDir = udc.homeDir
	}

	if flagSet.Changed("snapshotter") {
		daemonConfig.Snapshotter = udc.snapshotter
	}

	if flagSet.Changed("default-log-type") {
		daemonConfig.DefaultLogConfig.LogDriver = udc.defaultLogType
	}

	if flagSet.Changed("log-env") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["env"] = udc.logEnv
	}

	if flagSet.Changed("log-tag") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["tag"] = udc.logTag
	}

	if flagSet.Changed("syslog-address") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["syslog-address"] = udc.syslogAddress
	}

	if flagSet.Changed("syslog-facility") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["syslog-facility"] = udc.syslogFacility
	}

	if flagSet.Changed("syslog-format") {
		if daemonConfig.DefaultLogConfig.LogOpts == nil {
			daemonConfig.DefaultLogConfig.LogOpts = make(map[string]string)
		}
		daemonConfig.DefaultLogConfig.LogOpts["syslog-format"] = udc.syslogFormat
	}

	if flagSet.Changed("allow-multi-snapshotter") {
		daemonConfig.AllowMultiSnapshotter = udc.allowMultiSnapshotter
	}

	if flagSet.Changed("proxy-plugin") {
		if daemonConfig.ProxyPlugins == nil {
			daemonConfig.ProxyPlugins = make(map[string]map[string]string)
		}
		if daemonConfig.ProxyPlugins[udc.proxyPlugin] == nil {
			daemonConfig.ProxyPlugins[udc.proxyPlugin] = make(map[string]string)
		}
		if flagSet.Changed("proxy-plugin-address") {
			daemonConfig.ProxyPlugins[udc.proxyPlugin]["address"] = udc.proxyPluginAddress
		}
		if flagSet.Changed("proxy-plugin-type") {
			daemonConfig.ProxyPlugins[udc.proxyPlugin]["type"] = udc.proxyPluginType
		}
	}

	f, err := ioutil.TempFile(filepath.Dir(udc.configFile), ".tmp-"+filepath.Base(udc.configFile))
	if err != nil {
		return errors.Wrapf(err, "failed to create temp file")
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "    ")
	err = encoder.Encode(daemonConfig)
	if err != nil {
		return errors.Wrapf(err, "failed to write config to file(%s)", udc.configFile)
	}

	return os.Rename(f.Name(), udc.configFile)
}

// daemonUpdateExample shows examples in updatedaemon command, and is used in auto-generated cli docs.
func daemonUpdateExample() string {
	return `$ pouch updatedaemon --debug=true`
}
