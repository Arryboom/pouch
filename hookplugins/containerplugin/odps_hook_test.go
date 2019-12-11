package containerplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var testArrayStr = []string{
	`from subprocess import Popen, PIPE
from logging.handlers import RotatingFileHandler
from subprocess import call

routes = [
    "11.48.0.0/12","11.96.0.0/12","11.112.0.0/13","11.191.0.0/17","11.192.0.0/12", "11.208.0.0/12","11.236.0.0/16", "11.237.0.0/16","11.238.0.0/15","11.240.0.0/13","11.57.213.0/24","11.57.214.0/24","11.57.215.0/24","11.57.216.0/24","11.57.217.0/24"
]


class Logger(object):`,

	`from subprocess import Popen, PIPE
from logging.handlers import RotatingFileHandler
from subprocess import call

routes = [
    "11.48.0.0/12","11.96.0.0/12","11.112.0.0/13","11.191.0.0/17",
"11.192.0.0/12","11.208.0.0/12","11.236.0.0/16","11.237.0.0/16","11.238.0.0/15","11.240.0.0/13","11.57.213.0/24","11.57.214.0/24","11.57.215.0/24","11.57.216.0/24","11.57.217.0/24"]


class Logger(object):`,
}

var expectRoutes = []string{"11.48.0.0/12", "11.96.0.0/12", "11.112.0.0/13", "11.191.0.0/17", "11.192.0.0/12", "11.208.0.0/12", "11.236.0.0/16", "11.237.0.0/16",
	"11.238.0.0/15", "11.240.0.0/13", "11.57.213.0/24", "11.57.214.0/24", "11.57.215.0/24", "11.57.216.0/24", "11.57.217.0/24"}

func TestGetRoutesFromStr(t *testing.T) {
	for _, str := range testArrayStr {
		routes, err := getRoutesFromStr(str)
		assert.Nil(t, err)

		assert.Equal(t, len(routes), len(expectRoutes))

		compMap := make(map[string]struct{})
		for _, r := range routes {
			compMap[r] = struct{}{}
		}

		for _, r := range expectRoutes {
			_, exist := compMap[r]
			assert.Equal(t, exist, true)
			delete(compMap, r)
		}

		assert.Equal(t, len(compMap), 0)
	}
}
