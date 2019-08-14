package apiplugin

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

func Test_isClosed(t *testing.T) {
	type args struct {
		e error
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe error %v", err)
	}
	r.Close()
	w.Close()
	_, err = io.Copy(ioutil.Discard, r)

	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add test cases.
		{
			"not a PathErr",
			args{
				fmt.Errorf("other err"),
			},
			false,
		},
		{
			"is a not error",
			args{
				err,
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosed(tt.args.e); got != tt.want {
				t.Errorf("isClosed() = %v, want %v", got, tt.want)
			}
		})
	}
}
