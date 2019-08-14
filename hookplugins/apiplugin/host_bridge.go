package apiplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/pkg/stringid"
	"github.com/sirupsen/logrus"
)

var once sync.Once

type requestResult struct {
	Success  bool   `json:"success"`
	Msg      string `json:"msg"`
	Stderr   string `json:"stderr"`
	Stdout   string `json:"stdout"`
	ID       string `json:"id"`
	ExitCode int    `json:"exitCode"`
}

type callbackResult struct {
	Success  bool   `json:"success"`
	Done     bool   `json:"done"`
	Msg      string `json:"msg"`
	Stderr   string `json:"stderr"`
	Stdout   string `json:"stdout"`
	ExitCode int    `json:"exitCode"`
}

func init() {
	reexec.Register("docker-host-exec", runInNewSession)
}

func runInNewSession() {
	id := os.Args[1]
	async := os.Args[2]
	waitSecondStr := os.Args[3]
	command := exec.Command("bash", "/tmp/exec/"+id)

	stdErr, e := command.StderrPipe()

	if e != nil {
		fmt.Fprintf(os.Stderr, "open stderr stream error. %s, %v", id, e)
		os.Exit(1)
	}

	stdOut, _ := command.StdoutPipe()
	if e != nil {
		fmt.Fprintf(os.Stderr, "open stdout stream error. %s, %v", id, e)
		os.Exit(2)
	}

	if async == "true" {
		if waitSecondStr != "" {
			if waitSecond, e := strconv.Atoi(waitSecondStr); e == nil && waitSecond > 0 {
				time.Sleep(time.Second * time.Duration(waitSecond))
			}
		}
	}

	e = command.Start()

	if e != nil {
		fmt.Fprintf(os.Stderr, "%s exec failed. %v", id, e)
		os.Exit(3)
	}

	fstdout, e := os.Create("/tmp/exec/" + id + ".stdout")
	if e != nil {
		fmt.Fprintf(os.Stderr, "open stdout stream file error. %s, %v", id, e)
		os.Exit(4)
	}

	go func() {
		defer fstdout.Close()
		_, e := io.Copy(fstdout, stdOut)
		if e != nil && e != io.EOF && !strings.Contains(e.Error(), "file already closed") {
			fmt.Fprintf(os.Stderr, "copy stdout error. %s, %v", id, e)
		}
	}()
	ferrout, e := os.Create("/tmp/exec/" + id + ".stderr")
	if e != nil {
		fmt.Fprintf(os.Stderr, "open stderr stream file error. %s, %v", id, e)
		os.Exit(5)
	}
	go func() {
		defer ferrout.Close()
		_, e := io.Copy(ferrout, stdErr)
		if e != nil && e != io.EOF && !strings.Contains(e.Error(), "file already closed") {
			fmt.Fprintf(os.Stderr, "copy stderr error. %s, %v", id, e)
			fmt.Fprintf(ferrout, "\n read from stderr error. %v", e)
		}
	}()

	e = command.Wait()
	exitCode := 0
	if ex, ok := e.(*exec.ExitError); ok {
		exitCode = ex.Sys().(syscall.WaitStatus).ExitStatus()
	}
	fmt.Fprintf(os.Stdout, "host exec done %s %s %s", id, async, waitSecondStr)
	e = ioutil.WriteFile("/tmp/exec/"+id+".done", []byte(fmt.Sprintf("%d", exitCode)), 0777)
	if e != nil {
		fmt.Fprintf(os.Stderr, "write done file error. %s, %v", id, e)
		os.Exit(6)
	}
	os.Exit(exitCode)
}

func writeJSON(w http.ResponseWriter, obj interface{}) {
	b, e := json.Marshal(obj)
	if e != nil {
		logrus.Errorf("json marshal error. %v", e)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("json marshal error " + e.Error()))
		return
	}
	logrus.Debugf("write json %s", string(b))
	w.Write(b)
}

func writeEx(w http.ResponseWriter, obj requestResult, e error, extra string) {
	logrus.Errorf(extra+" error %v", e)
	obj.Msg = e.Error()
	obj.Success = false
	w.WriteHeader(http.StatusInternalServerError)
	writeJSON(w, obj)
}

func writeExCallback(w http.ResponseWriter, obj callbackResult, e error, extra string) {
	logrus.Errorf(extra+" error %v", e)
	obj.Msg = e.Error()
	obj.Success = false
	w.WriteHeader(http.StatusInternalServerError)
	writeJSON(w, obj)
}

// HostExecHandler is the handler for POST /host/exec
func HostExecHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		if err := recover(); err != nil {
			logrus.Errorf("host exec result recover from %v", err)
		}
	}()
	result := requestResult{}
	initClean()

	b, e := ioutil.ReadAll(r.Body)
	async := r.FormValue("async") == "true"

	if e != nil {
		logrus.Errorf("read host exec body error. %v", e)
		w.WriteHeader(http.StatusBadRequest)
		result.Msg = "read body error " + e.Error()
		writeJSON(w, result)
		return
	}

	id := stringid.GenerateNonCryptoID()
	result.ID = id
	e = ioutil.WriteFile("/tmp/exec/"+id, b, 0777)
	if e != nil {
		writeEx(w, result, e, id+" generate shell file")
		return
	}

	command := reexec.Command("docker-host-exec", id, strconv.FormatBool(async), r.FormValue("wait"))

	stdErr, e := command.StderrPipe()
	if e != nil {
		writeEx(w, result, e, id+" get stderr")
		return
	}

	stdOut, e := command.StdoutPipe()
	if e != nil {
		writeEx(w, result, e, id+" get stdout")
		return
	}

	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setsid = true
	command.SysProcAttr.Pdeathsig = syscall.Signal(0)

	e = command.Start()

	if e != nil {
		logrus.Errorf("failed. %v", e)
		w.WriteHeader(http.StatusServiceUnavailable)
		result.Msg = "exec failed.  " + e.Error()
		writeJSON(w, result)
		return
	}

	errChan := make(chan error, 1)
	finishChan := make(chan int)
	go func() {
		var stdBuffer bytes.Buffer
		n, e := io.Copy(&stdBuffer, stdOut)
		if (e == nil || e == io.EOF) && n > 0 {
			logrus.Infof("host exec sum process stdout %s, %s", id, string(stdBuffer.Bytes()))
		}
	}()
	go func() {
		var errBuffer bytes.Buffer
		n, e := io.Copy(&errBuffer, stdErr)
		if (e == nil || e == io.EOF) && n > 0 {
			errChan <- fmt.Errorf(string(errBuffer.Bytes()))
		}
		close(errChan)
	}()
	go func() {
		command.Wait()
		close(finishChan)
	}()

	if async {
		result.Success = true
		writeJSON(w, result)
		go func() {
			<-finishChan
			for ex := range errChan {
				if ex != nil {
					logrus.Errorf("%s command run error. %v", id, ex)
				}
			}
		}()
		return
	}

	<-finishChan
	for ex := range errChan {
		if ex != nil {
			logrus.Errorf("%s command run error. %v", id, ex)
			writeEx(w, result, ex, id+" exec")
			return
		}
	}

	stdByte, e := ioutil.ReadFile("/tmp/exec/" + id + ".stdout")
	if e != nil {
		writeEx(w, result, e, id+" read stdout file")
		return
	}

	errByte, e := ioutil.ReadFile("/tmp/exec/" + id + ".stderr")
	if e != nil {
		writeEx(w, result, e, id+" read stderr file")
		return
	}

	exitByte, e := ioutil.ReadFile("/tmp/exec/" + id + ".done")
	if e == nil {
		exitCode := strings.TrimSpace(string(exitByte))
		result.ExitCode, _ = strconv.Atoi(exitCode)
	}

	result.Success = true
	result.Stdout = string(stdByte)
	result.Stderr = string(errByte)
	writeJSON(w, result)

	return
}

// HostExecResultHandler is the handler for GET /host/exec/result
func HostExecResultHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		if err := recover(); err != nil {
			logrus.Errorf("host exec result recover from %v", err)
		}
	}()
	result := callbackResult{}

	id := r.FormValue("id")
	if len(id) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		result.Msg = "id is required"
		writeJSON(w, result)
		return
	}

	if fi, ex := os.Stat("/tmp/exec/" + id); ex != nil || fi.IsDir() {
		w.WriteHeader(http.StatusBadRequest)
		result.Msg = "id is not exist"
		writeJSON(w, result)
		return
	}

	if fi, ex := os.Stat("/tmp/exec/" + id + ".done"); ex == nil && !fi.IsDir() {
		result.Done = true
		if bArr, e := ioutil.ReadFile("/tmp/exec/" + id + ".done"); e == nil {
			exitCode := strings.TrimSpace(string(bArr))
			result.ExitCode, _ = strconv.Atoi(exitCode)
		}
	}

	stdoutFile := "/tmp/exec/" + id + ".stdout"
	if fi, ex := os.Stat(stdoutFile); ex == nil && !fi.IsDir() {
		b, e := ioutil.ReadFile(stdoutFile)
		if e != nil {
			writeExCallback(w, result, e, id+" read stdout file")
			return
		}
		result.Stdout = string(b)
	}

	stderrFile := "/tmp/exec/" + id + ".stderr"
	if fi, ex := os.Stat(stderrFile); ex == nil && !fi.IsDir() {
		b, e := ioutil.ReadFile(stderrFile)
		if e != nil {
			writeExCallback(w, result, e, id+" read stderr file")
			return
		}
		result.Stderr = string(b)
	}

	result.Success = true
	writeJSON(w, result)
	return
}

func initClean() {
	if _, ex := os.Stat("/tmp/exec"); ex != nil {
		os.MkdirAll("/tmp/exec", 0777)
	}

	once.Do(startClean)
}

func startClean() {
	go clean()
}

func clean() {
	ticker := time.Tick(time.Hour * 24 * 7)
	for range ticker {
		filepath.Walk("/tmp/exec", func(path string, info os.FileInfo, err error) error {
			if err == nil {
				if len(info.Name()) > 10 {
					if time.Since(info.ModTime()) > time.Hour*24*30 {
						os.RemoveAll(path)
					}
				}
			} else {
				logrus.Errorf("file walk error. %v", err)
			}
			return err
		})
	}
}
