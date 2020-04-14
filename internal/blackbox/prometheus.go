package blackbox

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	getFirstPacket          = newGauge("get_first_packet_seconds", "Time to first Git packet in GET /info/refs response")
	getTotalTime            = newGauge("get_total_time_seconds", "Time to receive entire GET /info/refs response")
	getAdvertisedRefs       = newGauge("get_advertised_refs", "Number of Git refs advertised in GET /info/refs")
	wantedRefs              = newGauge("wanted_refs", "Number of Git refs selected for (fake) Git clone (branches + tags)")
	postTotalTime           = newGauge("post_total_time_seconds", "Time to receive entire POST /upload-pack response")
	postFirstProgressPacket = newGauge("post_first_progress_packet_seconds", "Time to first progress band Git packet in POST /upload-pack response")
	postFirstPackPacket     = newGauge("post_first_pack_packet_seconds", "Time to first pack band Git packet in POST /upload-pack response")
	postPackBytes           = newGauge("post_pack_bytes", "Number of pack band bytes in POST /upload-pack response")
)

func newGauge(name string, help string) *prometheus.GaugeVec {
	return promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gitaly_blackbox",
			Subsystem: "git_http",
			Name:      name,
			Help:      help,
		},
		[]string{"probe"},
	)
}
