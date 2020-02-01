package mount

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestIsLikelyNotMountPoint(t *testing.T) {
	type args struct {
		file string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsLikelyNotMountPoint(tt.args.file)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsLikelyNotMountPoint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsLikelyNotMountPoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsMount(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "test-is-mount")
	if err != nil {
		t.Errorf("failed to create tmpDir %s", err)
	}
	defer func() { os.RemoveAll(tmpDir) }()

	type Case struct {
		path      string
		isMounted bool
	}
	tCase := []Case{
		{filepath.Join(tmpDir, "none-mount"), false},
	}

	// append some alread mount path
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		t.Skip("can not get mountinfo")
	}

	s := bufio.NewScanner(f)
	for s.Scan() {
		var skipInt int
		var ftype, source, skipString string
		fmt.Sscanf(s.Text(), "%d %d %d:%d %s %s %s",
			&skipInt, &skipInt, &skipInt, &skipInt, &ftype, &source, &skipString)

		if _, err := os.Stat(source); err == nil {
			tCase = append(tCase, Case{source, true})
		}
	}

	for _, tc := range tCase {
		t.Logf("check %s", tc.path)
		isMounted := IsMounted(tc.path)
		if tc.isMounted != isMounted {
			t.Errorf("check %s should be %v, but get %v", tc.path, tc.isMounted, isMounted)
		}
	}
}
