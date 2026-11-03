package stream

import (
	"strconv"
	"strings"
	"time"

	"arlo-term/internal/arlo"
)

const (
	CertHost       = "stream.wowza.arlo.com"
	ConnectTimeout = 10 * time.Second
	ErrorRetry     = time.Minute

	retryDelay   = 2 * time.Second
	deniedRetry  = 5 * time.Second
	offlineRetry = 30 * time.Second
	deniedLimit  = 3
)

type Status string

const (
	Connecting   Status = "connecting"
	Live         Status = "live"
	Reconnecting Status = "reconnecting"
	Offline      Status = "offline"
	Error        Status = "error"
	Closed       Status = "closed"
)

type URLFunc func(arlo.Camera) (string, error)

var FFmpegInput = []string{
	"-timeout", strconv.Itoa(int(ConnectTimeout / time.Microsecond)),
	"-rtsp_transport", "tcp", "-verifyhost", CertHost,
	"-fflags", "nobuffer", "-flags", "low_delay",
}

type RetryPolicy struct {
	denied int
}

func (r *RetryPolicy) Next(log string, failed bool) (Status, time.Duration) {
	if !failed && !strings.Contains(log, "Forbidden") {
		r.denied = 0
		return Reconnecting, retryDelay
	}
	r.denied++
	if r.denied >= deniedLimit {
		return Offline, offlineRetry
	}
	return Connecting, deniedRetry
}

func Sleep(d time.Duration, stop <-chan struct{}) bool {
	select {
	case <-time.After(d):
		return true
	case <-stop:
		return false
	}
}
