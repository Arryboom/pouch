package apiplugin

import (
	"bytes"
	"context"
	"io/ioutil"
	"net"
	"net/http"

	"github.com/alibaba/pouch/pkg/log"

	"github.com/pkg/errors"
)

func alinetDial(proto, addr string) (conn net.Conn, err error) {
	return net.Dial("unix", "/run/docker/plugins/alinet/alinet.sock")
}

// NetworkExtendHandler is the handler for POST /networks/extend
func NetworkExtendHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) (err error) {
	reader := r.Body
	request, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	log.With(ctx).Infof("call network extend(%q)", request)

	// post to alinet network plugin
	client := &http.Client{
		Transport: &http.Transport{
			Dial: alinetDial,
		},
	}

	alinetResp, err := client.Post("http://localhost.localdomain/extend", r.Header.Get("Content-Type"), bytes.NewReader(request))
	if err != nil {
		return errors.Wrap(err, "failed to post cni network request")
	}

	data, err := ioutil.ReadAll(alinetResp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read from response body")
	}
	defer alinetResp.Body.Close()

	if alinetResp.StatusCode != http.StatusOK {
		log.With(ctx).Errorf("failed to call network extend, code(%d)", alinetResp.StatusCode)
	}

	if len(data) == 0 {
		data = nil
		log.With(ctx).Infof("end of call network extend, data is nil")
	} else {
		log.With(ctx).Infof("end of call network extend, data(%q)", data)
	}

	w.Header().Set("Content-Type", alinetResp.Header.Get("Content-Type"))
	w.WriteHeader(alinetResp.StatusCode)
	w.Write(data)

	return nil
}
