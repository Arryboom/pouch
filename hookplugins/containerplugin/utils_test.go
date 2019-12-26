package containerplugin

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/daemon/mgr"
	"github.com/stretchr/testify/assert"
)

func TestUniqueStringSlice(t *testing.T) {
	cases := []struct {
		input    []string
		expected []string
	}{
		{
			input:    []string{},
			expected: []string{},
		}, {
			input:    []string{"1"},
			expected: []string{"1"},
		}, {
			input:    []string{"3", "1", "2", "1", "2"},
			expected: []string{"1", "2", "3"},
		}, {
			input:    []string{"3", "1", "2"},
			expected: []string{"1", "2", "3"},
		},
	}

	for _, tc := range cases {
		got := UniqueStringSlice(tc.input)
		sort.Strings(got)
		sort.Strings(tc.expected)
		if !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}

func TestAddEnvironment(t *testing.T) {
	image := "reg.docker.alibaba-inc.com/ali/os:7u2"
	id := "dddddd"
	env := []string{}
	env = addEnvironment(image, id, "runc", env)
	if len(env) != 3 {
		t.Fatalf("expect length 2, but got %d", len(env))
	}
	if env[0] != "pouch_container_image=reg.docker.alibaba-inc.com/ali/os:7u2" {
		t.Fatalf("expect (pouch_container_image=reg.docker.alibaba-inc.com/ali/os:7u2), but got %s", env[0])
	}
	if env[1] != "pouch_container_id=dddddd" {
		t.Fatalf("expect (pouch_container_id=dddddd), but got %s", env[1])
	}
	if env[2] != "ali_runtime_type=runc" {
		t.Fatalf("expect (ali_runtime_type=runc), but got %s", env[2])
	}
}

func TestCreateEnvForCopyPodHosts(t *testing.T) {
	cases := []struct {
		caseName  string
		runtime   string
		binds     []string
		setHosts  bool
		setResolv bool
	}{
		{
			caseName: "runc with resolv.conf",
			runtime:  "runc",
			binds:    []string{"/test/hosts:/etc/hosts", "/test/resolv.conf:/etc/resolv.conf:rw"},
		},
		{
			caseName: "runc without resolv.conf",
			runtime:  "runc",
			binds:    []string{"/test/hostname:/etc/hostname"},
		},
		{
			caseName:  "kata with resolv.conf and hosts with rw",
			runtime:   "kata-runtime",
			binds:     []string{"/test/hosts:/etc/hosts:rw", "/test/resolv.conf:/etc/resolv.conf:rw", "/test/hostname:/etc/hostname"},
			setResolv: true,
			setHosts:  true,
		},
		{
			caseName:  "kata with resolv.conf",
			runtime:   "kata-runtime",
			binds:     []string{"/test/resolv.conf:/etc/resolv.conf", "/test/hostname:/etc/hostname"},
			setResolv: true,
		},
		{
			caseName: "kata without resolv.conf",
			runtime:  "kata-runtime",
			binds:    []string{"/test/hosts:/etc/hosts", "/test/hostname:/etc/hostname"},
		},
		{
			caseName:  "kata with resolv.conf and hosts",
			runtime:   "kata-runtime",
			binds:     []string{"/test/hosts:/etc/hosts", "/test/resolv.conf:/etc/resolv.conf", "/test/hostname:/etc/hostname"},
			setResolv: true,
			setHosts:  true,
		},
	}
	for _, tc := range cases {
		cont := &mgr.Container{
			HostConfig: &types.HostConfig{
				Runtime: tc.runtime,
				Binds:   tc.binds,
			},
			Config: &types.ContainerConfig{
				Env: []string{},
			},
		}

		if tc.runtime == kataRuntimeClass {
			err := createEnvForCopyPodHosts(cont.Config, cont.HostConfig)
			assert.Nil(t, err)
		}

		value := getEnv(cont.Config.Env, "CopyPodHostsArgs")
		if !tc.setResolv {
			assert.Equal(t, "", value, fmt.Sprintf("%s", tc.caseName))
			continue
		}

		args := strings.Split(value, ",")
		assert.Equal(t, 5, len(args), tc.caseName)
		assert.Equal(t, "/opt/ali-iaas/pouch/bin/prestart_hook_alipay", args[0], tc.caseName)
		assert.Equal(t, "CopyPodHosts", args[1], tc.caseName)
		assert.Equal(t, "/test/resolv.conf", args[2], tc.caseName)
		assert.Equal(t, noneStr, args[4], tc.caseName)
		if tc.setHosts {
			assert.Equal(t, "/test/hosts", args[3], tc.caseName)
		} else {
			assert.Equal(t, noneStr, args[3], tc.caseName)
		}
	}
}
