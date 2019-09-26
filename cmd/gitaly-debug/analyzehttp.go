package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"os"
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
	msg("---")
	msg("--- GET %v", req.URL)
	msg("---")
	resp, err := http.DefaultClient.Do(req)
	noError(err)

	msg("response after %v", time.Since(start))
	msg("response header: %v", resp.Header)
	msg("HTTP status code %d", resp.StatusCode)
	defer resp.Body.Close()

	body := resp.Body
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		body, err = gzip.NewReader(body)
		noError(err)
	}

	// Expected response:
	// - "# service=git-upload-pack\n"
	// - FLUSH
	// - "<OID> <ref> <capabilities>\n"
	// - "<OID> <ref>\n"
	// - ...
	// - FLUSH
	//
	var wants []string
	var size int64
	seenFlush := false
	scanner := pktline.NewScanner(body)
	packets := 0
	refs := 0
	for ; scanner.Scan(); packets++ {
		if seenFlush {
			fatal("received packet after flush")
		}

		data := string(pktline.Data(scanner.Bytes()))
		size += int64(len(data))
		switch packets {
		case 0:
			msg("first packet %v", time.Since(start))
			if data != "# service=git-upload-pack\n" {
				fatal(fmt.Errorf("unexpected header %q", data))
			}
		case 1:
			if !pktline.IsFlush(scanner.Bytes()) {
				fatal("missing flush after service announcement")
			}
		default:
			if packets == 2 && !strings.Contains(data, " side-band-64k") {
				fatal(fmt.Errorf("missing side-band-64k capability in %q", data))
			}

			if pktline.IsFlush(scanner.Bytes()) {
				seenFlush = true
				continue
			}

			split := strings.SplitN(data, " ", 2)
			if len(split) != 2 {
				continue
			}
			refs++

			if strings.HasPrefix(split[1], "refs/heads/") || strings.HasPrefix(split[1], "refs/tags/") {
				wants = append(wants, split[0])
			}
		}
	}
	noError(scanner.Err())
	if !seenFlush {
		fatal("missing flush in response")
	}

	msg("received %d packets", packets)
	msg("done in %v", time.Since(start))
	msg("payload data: %d bytes", size)
	msg("received %d refs, selected %d wants", refs, len(wants))

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
		"Content-Type":     "application/x-git-upload-pack-request",
		"Accept":           "application/x-git-upload-pack-result",
		"Content-Encoding": "gzip",
	} {
		req.Header.Set(k, v)
	}

	start := time.Now()
	msg("---")
	msg("--- POST %v", req.URL)
	msg("---")
	resp, err := http.DefaultClient.Do(req)
	noError(err)

	msg("response after %v", time.Since(start))
	msg("response header: %v", resp.Header)
	msg("HTTP status code %d", resp.StatusCode)
	defer resp.Body.Close()

	// Expected response:
	// - "NAK\n"
	// - "<side band byte><pack or progress or error data>
	// - ...
	// - FLUSH
	//
	packets := 0
	scanner := pktline.NewScanner(resp.Body)
	totalSize := make(map[byte]int64)
	payloadSizeHistogram := make(map[int]int)
	sideBandHistogram := make(map[byte]int)
	seenFlush := false
	for ; scanner.Scan(); packets++ {
		if seenFlush {
			fatal("received extra packet after flush")
		}

		data := pktline.Data(scanner.Bytes())

		if packets == 0 {
			if !bytes.Equal([]byte("NAK\n"), data) {
				fatal(fmt.Errorf("expected NAK, got %q", data))
			}
			msg("received NAK after %v", time.Since(start))
			continue
		}

		if pktline.IsFlush(scanner.Bytes()) {
			seenFlush = true
			continue
		}

		if len(data) == 0 {
			fatal("empty packet in PACK data")
		}

		band := data[0]
		if band < 1 || band > 3 {
			fatal(fmt.Errorf("invalid sideband: %d", band))
		}
		if sideBandHistogram[band] == 0 {
			msg("received first %s packet after %v", bandToHuman(band), time.Since(start))
		}

		sideBandHistogram[band]++

		// Print progress data as-is
		if band == 2 {
			_, err := os.Stdout.Write(data[1:])
			noError(err)
		}

		n := len(data[1:])
		totalSize[band] += int64(n)
		payloadSizeHistogram[n]++

		if packets%100 == 0 && packets > 0 && band == 1 {
			fmt.Printf(".")
		}
	}

	fmt.Println("") // Trailing newline for progress dots.

	noError(scanner.Err())
	if !seenFlush {
		fatal("POST response did not end in flush")
	}

	msg("received %d packets", packets)
	msg("done in %v", time.Since(start))
	for i := byte(1); i <= 3; i++ {
		msg("%8s band: %10d payload bytes, %6d packets", bandToHuman(i), totalSize[i], sideBandHistogram[i])
	}
	msg("packet payload size histogram: %v", payloadSizeHistogram)
}

func bandToHuman(b byte) string {
	switch b {
	case 1:
		return "pack"
	case 2:
		return "progress"
	case 3:
		return "error"
	default:
		fatal(fmt.Errorf("invalid band %d", b))
		return "" // never reached
	}
}
