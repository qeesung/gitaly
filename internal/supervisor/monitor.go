package supervisor

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	rssGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gitaly_supervisor_rss_bytes",
			Help: "Resident set size of supervised processes, in bytes.",
		},
		[]string{"name"},
	)
)

func init() {
	prometheus.MustRegister(rssGauge)
}

type monitorProcess struct {
	name string
	pid  int
	wait <-chan struct{}
}

func monitorRss(procs <-chan monitorProcess, done chan<- struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()

	defer close(done)

	for mp := range procs {
	monitorLoop:
		for {
			rssGauge.WithLabelValues(mp.name).Set(float64(1024 * getRss(mp.pid)))

			select {
			case <-mp.wait:
				break monitorLoop
			case <-t.C:
			}
		}
	}
}

// getRss returns RSS in kilobytes.
func getRss(pid int) int {
	// I tried adding a library to do this but it seemed like overkill
	// and YAGNI compared to doing this one 'ps' call.
	psRss, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}

	rss, err := strconv.Atoi(strings.TrimSpace(string(psRss)))
	if err != nil {
		return 0
	}

	return rss
}
