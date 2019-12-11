package containerplugin

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/alibaba/pouch/apis/types"
)

const (
	odpsRegStr = `routes\s*=\s*\[\s*(("((2[0-4]\d|25[0-5]|[01]?\d\d?)\.){3}(2[0-4]\d|25[0-5]|[01]?\d\d?)/\d+")\s*,?\s*)+\s*]`
)

func getRoutesFromStr(input string) ([]string, error) {
	reg := regexp.MustCompile(odpsRegStr)
	s := reg.FindStringSubmatch(input)
	if len(s) < 1 {
		return nil, fmt.Errorf("failed to match regexp")
	}
	// s should be like this: `routes = [ "1.1.1.1" , "2.2.2.2" ] `
	leftArrayPattern := strings.Index(s[0], "[")
	rightArrayPattern := strings.Index(s[0], "]")

	if leftArrayPattern < 0 || rightArrayPattern < 0 {
		return nil, fmt.Errorf("failed to found [ ] in %s", s)
	}

	// routesStr string like this: `"1,1,1,1", "2.2.2.2"`
	routesStr := s[0][leftArrayPattern+1 : rightArrayPattern]
	routes := strings.Split(strings.TrimSpace(routesStr), ",")

	ret := []string{}
	for _, r := range routes {
		ret = append(ret, strings.Trim(strings.TrimSpace(r), "\""))
	}

	return ret, nil
}

func getURLResult(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("failed to get url %s, status code %d", url, resp.StatusCode)
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func getUnderlayRouteURL(config types.ContainerConfig) string {
	const odpsRouteGetURL = "http://100.100.100.200/latest/user-data"
	const routeURLEnv = "ODPS_UNDERLAY_ROUTE_GET_URL"
	if v := getEnv(config.Env, routeURLEnv); v != "" {
		return v
	}

	return odpsRouteGetURL
}

func getUnderlayIPURL(config types.ContainerConfig) string {
	const odpsIPGetURL = "http://100.100.100.200/latest/meta-data/phynic/ip"
	const IPURLEnv = "ODPS_UNDERLAY_IP_GET_URL"
	if v := getEnv(config.Env, IPURLEnv); v != "" {
		return v
	}

	return odpsIPGetURL
}

func getUnderlayGatewayURL(config types.ContainerConfig) string {
	const odpsGatewayGetURL = "http://100.100.100.200/latest/meta-data/phynic/gateway"
	const GatewayURLEnv = "ODPS_UNDERLAY_GATEWAY_GET_URL"
	if v := getEnv(config.Env, GatewayURLEnv); v != "" {
		return v
	}

	return odpsGatewayGetURL
}

func getUnderlayMACURL(config types.ContainerConfig) string {
	const odpsMACGetURL = "http://100.100.100.200/latest/meta-data/phynic/mac"
	const MacURLEnv = "ODPS_UNDERLAY_MAC_GET_URL"
	if v := getEnv(config.Env, MacURLEnv); v != "" {
		return v
	}

	return odpsMACGetURL
}

func runOdpsHook(createConfig *types.ContainerCreateConfig) error {
	var (
		underlayIP      string
		underlayGateway string
		underlayMac     string
		underlayRoutes  string
		err             error
	)

	const (
		underlayIPEnv     = "ODPS_UNDERLAY_IP"
		underlayGateEnv   = "ODPS_UNDERLAY_GATEWAY"
		underlayMACEnv    = "ODPS_UNDERLAY_MAC"
		underlayRoutesEnv = "ODPS_UNDERLAY_ROUTES"
	)

	// get underlay ip
	underlayIP, err = getURLResult(getUnderlayIPURL(createConfig.ContainerConfig))
	if err != nil {
		return fmt.Errorf("failed to get ip: %v", err)
	}

	// get underlay gateway
	underlayGateway, err = getURLResult(getUnderlayGatewayURL(createConfig.ContainerConfig))
	if err != nil {
		return fmt.Errorf("failed to get gateway: %v", err)
	}

	// get underlay mac
	underlayMac, err = getURLResult(getUnderlayMACURL(createConfig.ContainerConfig))
	if err != nil {
		return fmt.Errorf("failed to get mac: %v", err)
	}

	//get underlay route
	routeString, err := getURLResult(getUnderlayRouteURL(createConfig.ContainerConfig))
	if err != nil {
		return fmt.Errorf("failed to get route string: %v", err)
	}

	routes, err := getRoutesFromStr(routeString)
	if err != nil {
		return fmt.Errorf("failed to get routes: %v", err)
	}

	underlayRoutes = strings.Join(routes, ",")

	// set underlay ip,gateway,mac,routes to env
	if getEnv(createConfig.Env, underlayIPEnv) == "" {
		createConfig.Env = append(createConfig.Env, fmt.Sprintf("%s=%s", underlayIPEnv, underlayIP))
	}

	if getEnv(createConfig.Env, underlayGateEnv) == "" {
		createConfig.Env = append(createConfig.Env, fmt.Sprintf("%s=%s", underlayGateEnv, underlayGateway))
	}

	if getEnv(createConfig.Env, underlayMACEnv) == "" {
		createConfig.Env = append(createConfig.Env, fmt.Sprintf("%s=%s", underlayMACEnv, underlayMac))
	}

	if getEnv(createConfig.Env, underlayRoutesEnv) == "" {
		createConfig.Env = append(createConfig.Env, fmt.Sprintf("%s=%s", underlayRoutesEnv, underlayRoutes))
	}

	return nil
}
