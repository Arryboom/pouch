package syslog

import (
	"crypto/tls"
	"errors"
	"net"
	"time"
)

type serverConn interface {
	writeString(framer Framer, formatter Formatter, p Priority, hostname, tag, s string) error
	updateWriteTimeout(time.Duration)
	close() error
}

func makeDialer(proto string, addr string, cfg *tls.Config) (serverConn, string, error) {
	switch proto {
	case "":
		return unixLocalDialer()
	case secureProto:
		return tlsDialer(addr, cfg)
	default:
		return commonDialer(proto, addr)
	}
}

// commonDialer is the most common dialer for TCP/UDP/Unix connections.
func commonDialer(network string, addr string) (serverConn, string, error) {
	var (
		sc       serverConn
		hostname string
	)

	c, err := net.DialTimeout(network, addr, dailTimeoutSec*time.Second)
	if err == nil {
		sc = &remoteConn{
			conn:         c,
			writeTimeout: writeTimeoutSec,
		}
		hostname = c.LocalAddr().String()
	}
	return sc, hostname, err
}

// tlsDialer connects to TLS over TCP, and is used for the "tcp+tls" network.
func tlsDialer(addr string, cfg *tls.Config) (serverConn, string, error) {
	var (
		sc       serverConn
		hostname string
	)

	dailer := &net.Dialer{
		Timeout: dailTimeoutSec,
	}

	c, err := tls.DialWithDialer(dailer, "tcp", addr, cfg)
	if err == nil {
		sc = &remoteConn{
			conn:         c,
			writeTimeout: writeTimeoutSec,
		}
		hostname = c.LocalAddr().String()
	}
	return sc, hostname, err
}

// unixLocalDialer opens a Unix domain socket connection to the syslog daemon
// running on the local machine.
func unixLocalDialer() (serverConn, string, error) {
	for _, network := range unixDialerTypes {
		for _, path := range unixDialerLocalPaths {
			conn, err := net.DialTimeout(network, path, dailTimeoutSec*time.Second)
			if err == nil {
				return &localConn{
					conn:         conn,
					writeTimeout: writeTimeoutSec,
				}, "localhost", nil
			}
		}
	}
	return nil, "", errors.New("unix local syslog delivery error")
}
