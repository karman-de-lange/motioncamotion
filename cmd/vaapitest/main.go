package main

/*
#cgo CFLAGS: -I/usr/include/vpl
#cgo LDFLAGS: -lmfx-gen -lstdc++

#include <stdlib.h>
#include "vpl_decode.h"
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"
)

type Camera struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type motionStats struct {
	name         string
	frames       int
	sadTriggers  int
	hashTriggers int
	maxSad       float64
	maxHamming   int
}

func annexB(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		b.Write([]byte{0, 0, 0, 1})
		b.Write(p)
	}
	return b.Bytes()
}

func cpuPercent() float64 {
	f, err := os.Open("/proc/self/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	line, err := io.ReadAll(f)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(line))
	if len(fields) < 15 {
		return 0
	}
	utime, _ := strconv.ParseUint(fields[13], 10, 64)
	stime, _ := strconv.ParseUint(fields[14], 10, 64)
	return float64(utime+stime) / 100.0
}

func runCamera(cam Camera, stop <-chan struct{}) motionStats {
	stats := motionStats{name: cam.Name}

	var dec *C.VPLDecoder
	if rc := C.vpl_decoder_new(&dec); rc != 0 {
		fmt.Fprintf(os.Stderr, "%s: vpl init %d\n", cam.Name, int(rc))
		return stats
	}
	defer C.vpl_decoder_free(dec)

	u, err := base.ParseURL(cam.URL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}

	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}

	var forma *format.H265
	medi := desc.FindFormat(&forma)
	if medi == nil {
		fmt.Fprintf(os.Stderr, "%s: no H265 stream\n", cam.Name)
		return stats
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}

	var prevY [thumbSize]byte
	var prevHash uint64
	var havePrev bool
	var loggedErr bool
	thumb := make([]byte, thumbSize)

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		select {
		case <-stop:
			return
		default:
		}

		au, err := rtpDec.Decode(pkt)
		if err != nil {
			if errors.Is(err, rtph265.ErrMorePacketsNeeded) || errors.Is(err, rtph265.ErrNonStartingPacketAndNoPrevious) {
				return
			}
			return
		}

		parts := [][]byte{forma.VPS, forma.SPS, forma.PPS}
		parts = append(parts, au...)
		bitstream := annexB(parts...)

		rc := C.vpl_decoder_thumb(dec, (*C.uint8_t)(unsafe.Pointer(&bitstream[0])), C.size_t(len(bitstream)),
			(*C.uint8_t)(unsafe.Pointer(&thumb[0])), C.int(len(thumb)))
		if rc == 1 {
			return
		}
		if rc != 0 {
			if !loggedErr {
				fmt.Fprintf(os.Stderr, "%s: thumb error %d\n", cam.Name, int(rc))
				loggedErr = true
			}
			return
		}

		stats.frames++
		if !havePrev {
			copy(prevY[:], thumb)
			prevHash = pHash(prevY[:])
			havePrev = true
			return
		}

		sad := sadPct(prevY[:], thumb)
		hash := pHash(thumb)
		dist := hamming(prevHash, hash)

		if sad > stats.maxSad {
			stats.maxSad = sad
		}
		if dist > stats.maxHamming {
			stats.maxHamming = dist
		}
		if sad >= sadThreshold {
			stats.sadTriggers++
		}
		if dist >= hashThreshold {
			stats.hashTriggers++
		}

		copy(prevY[:], thumb)
		prevHash = hash
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cam.Name, err)
		return stats
	}

	go func() {
		<-stop
		c.Close()
	}()

	_ = c.Wait()
	return stats
}

func main() {
	cfgPath := "cameras.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	var cfg struct {
		Cameras []Camera `json:"cameras"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Cameras) == 0 {
		fmt.Fprintln(os.Stderr, "no cameras")
		os.Exit(1)
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	startCPU := cpuPercent()
	startAt := time.Now()

	var wg sync.WaitGroup
	results := make(chan motionStats, len(cfg.Cameras))

	for _, cam := range cfg.Cameras {
		wg.Add(1)
		go func(cam Camera) {
			defer wg.Done()
			results <- runCamera(cam, stop)
		}(cam)
	}

	go func() {
		<-sig
		close(stop)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Printf("thresholds: sad>=%.0f%% pixels (noise>%d)  hamming>=%d bits\n",
		sadThreshold, noiseFloor, hashThreshold)

	for s := range results {
		fmt.Printf("%s: frames=%d  sad_triggers=%d  hash_triggers=%d  max_sad=%.1f%%  max_hamming=%d\n",
			s.name, s.frames, s.sadTriggers, s.hashTriggers, s.maxSad, s.maxHamming)
	}

	elapsed := time.Since(startAt).Seconds()
	cpu := (cpuPercent() - startCPU) / elapsed * 100
	fmt.Printf("cpu avg: %.1f%%\n", cpu)
}