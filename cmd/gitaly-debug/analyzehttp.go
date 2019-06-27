package main

import (
	"bytes"
	"compress/gzip"
	"log"
	"net/http"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/git/pktline"
)

func analyzeHTTPClone(cloneURL string) {
	wants := doBenchGet(cloneURL)
	doBenchPost(cloneURL, wants)
}

func doBenchGet(cloneURL string) []string {
	req, err := http.NewRequest("GET", cloneURL+"/info/refs?service=git-upload-pack", nil)
	noError(err)

	for k, v := range map[string]string{
		"User-Agent":      "gitaly-debug",
		"Accept":          "*/*",
		"Accept-Encoding": "deflate, gzip",
		"Pragma":          "no-cache",
	} {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	noError(err)

	log.Printf("GET response header: %v", resp.Header)
	defer resp.Body.Close()

	var wants []string
	var size int64
	scanner := pktline.NewScanner(resp.Body)
	for i := 0; scanner.Scan(); i++ {
		data := pktline.Data(scanner.Bytes())
		size += int64(len(data))
		if i == 0 {
			log.Printf("GET first packet %v", time.Since(start))
			continue
		}

		split := strings.SplitN(string(data), " ", 2)
		if len(split) != 2 {
			continue
		}

		if strings.HasPrefix(split[1], "refs/heads/") || strings.HasPrefix(split[1], "refs/tags/") {
			wants = append(wants, split[0])
		}
	}
	noError(scanner.Err())
	log.Printf("GET done %v", time.Since(start))
	log.Printf("GET data %d bytes", size)

	return wants
}

func doBenchPost(cloneURL string, wants []string) {
	reqBodyRaw := &bytes.Buffer{}
	reqBodyGzip := gzip.NewWriter(reqBodyRaw)
	for i, oid := range wants {
		if i == 0 {
			oid += " multi_ack_detailed no-done side-band-64k thin-pack ofs-delta deepen-since deepen-not agent=git/2.21.0"
		}
		_, err := pktline.WriteString(reqBodyGzip, "want "+oid+"\n")
		noError(err)
	}
	noError(pktline.WriteFlush(reqBodyGzip))
	_, err := pktline.WriteString(reqBodyGzip, "done\n")
	noError(err)
	noError(reqBodyGzip.Close())

	req, err := http.NewRequest("POST", cloneURL+"/git-upload-pack", reqBodyRaw)
	noError(err)

	for k, v := range map[string]string{
		"User-Agent":       "gitaly-debug",
		"Accept-Encoding":  "deflate, gzip",
		"Content-Type":     "application/x-git-upload-pack-request",
		"Accept":           "application/x-git-upload-pack-result",
		"Content-Encoding": "gzip",
	} {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	noError(err)
	log.Printf("POST response %v", time.Since(start))

	log.Printf("POST response header: %v", resp.Header)
	log.Printf("POST code %d", resp.StatusCode)
	defer resp.Body.Close()

	packets := 0
	scanner := pktline.NewScanner(resp.Body)
	var size int64
	sizeHistogram := make(map[int]int)
	sideBandHistogram := make(map[byte]int)
	for ; scanner.Scan(); packets++ {
		if packets == 0 {
			log.Printf("POST first packet %v", time.Since(start))
		}

		data := pktline.Data(scanner.Bytes())
		n := len(data)
		size += int64(n)
		sizeHistogram[n]++
		if len(data) > 0 {
			sideBandHistogram[data[0]]++
		}
	}
	noError(scanner.Err())

	log.Printf("POST: %d packets", packets)
	log.Printf("POST done %v", time.Since(start))
	log.Printf("POST data %d bytes", size)
	log.Printf("POST packet size histogram: %v", sizeHistogram)
	log.Printf("POST packet sideband histogram: %v", sideBandHistogram)
}
