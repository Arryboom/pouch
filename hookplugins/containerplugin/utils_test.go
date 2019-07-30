package containerplugin

import (
	"reflect"
	"sort"
	"testing"
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
