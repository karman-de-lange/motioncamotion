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
	"sync/atomic"
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
	Main string `json:"main"`
	Sub  string `json:"sub"`
}

type ringBuffer struct {
	mu       sync.Mutex
	chunks   [][]byte
	total    int
	maxBytes int
}

func (r *ringBuffer) push(data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.chunks = append(r.chunks, cp)
	r.total += len(cp)
	for r.total > r.maxBytes && len(r.chunks) > 0 {
		r.total -= len(r.chunks[0])
		r.chunks = r.chunks[1:]
	}
}

func (r *ringBuffer) bytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

type camStats struct {
	name         string
	mainAUs      int
	bufBytes     int
	motionFrames int
	sadTriggers  int
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

func cpuTicks() float64 {
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
	return float64(utime + stime)
}

func runMain(cam Camera, ring *ringBuffer, stop <-chan struct{}, mainAUs *atomic.Int64) {
	u, err := base.ParseURL(cam.Main)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}

	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}

	var forma *format.H265
	medi := desc.FindFormat(&forma)
	if medi == nil {
		fmt.Fprintf(os.Stderr, "%s main: no H265\n", cam.Name)
		return
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}

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
		ring.push(annexB(parts...))
		mainAUs.Add(1)
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s main: %v\n", cam.Name, err)
		return
	}

	go func() {
		<-stop
		c.Close()
	}()

	_ = c.Wait()
}

func runSub(cam Camera, stop <-chan struct{}, stats *camStats) {
	var dec *C.VPLDecoder
	if rc := C.vpl_decoder_new(&dec); rc != 0 {
		fmt.Fprintf(os.Stderr, "%s sub: vpl init %d\n", cam.Name, int(rc))
		return
	}
	defer C.vpl_decoder_free(dec)

	u, err := base.ParseURL(cam.Sub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}

	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
	if err := c.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}

	var forma *format.H265
	medi := desc.FindFormat(&forma)
	if medi == nil {
		fmt.Fprintf(os.Stderr, "%s sub: no H265\n", cam.Name)
		return
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}

	var prevY [thumbSize]byte
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
				fmt.Fprintf(os.Stderr, "%s sub: thumb error %d\n", cam.Name, int(rc))
				loggedErr = true
			}
			return
		}

		stats.motionFrames++
		if !havePrev {
			copy(prevY[:], thumb)
			havePrev = true
			return
		}

		if sadPct(prevY[:], thumb) >= sadThreshold {
			stats.sadTriggers++
		}
		copy(prevY[:], thumb)
	})

	if _, err = c.Play(nil); err != nil {
		fmt.Fprintf(os.Stderr, "%s sub: %v\n", cam.Name, err)
		return
	}

	go func() {
		<-stop
		c.Close()
	}()

	_ = c.Wait()
}

func runCamera(cam Camera, ring *ringBuffer, stop <-chan struct{}) camStats {
	stats := camStats{name: cam.Name}
	var mainAUs atomic.Int64

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runMain(cam, ring, stop, &mainAUs)
	}()
	go func() {
		defer wg.Done()
		runSub(cam, stop, &stats)
	}()
	wg.Wait()

	stats.mainAUs = int(mainAUs.Load())
	stats.bufBytes = ring.bytes()
	return stats
}

func main() {
	cfgPath := "cameras_dual_7.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	var cfg struct {
		BufferMB int      `json:"buffer_mb"`
		Cameras  []Camera `json:"cameras"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Cameras) == 0 {
		fmt.Fprintln(os.Stderr, "no cameras")
		os.Exit(1)
	}
	if cfg.BufferMB <= 0 {
		cfg.BufferMB = 200
	}
	maxBuf := cfg.BufferMB << 20

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	startCPU := cpuTicks()
	startAt := time.Now()

	rings := make([]*ringBuffer, len(cfg.Cameras))
	for i := range cfg.Cameras {
		rings[i] = &ringBuffer{maxBytes: maxBuf}
	}

	var wg sync.WaitGroup
	results := make(chan camStats, len(cfg.Cameras))

	for i, cam := range cfg.Cameras {
		wg.Add(1)
		go func(cam Camera, ring *ringBuffer) {
			defer wg.Done()
			results <- runCamera(cam, ring, stop)
		}(cam, rings[i])
	}

	go func() {
		<-sig
		close(stop)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Printf("dual: main buffer=%dMB/cam  sub motion  cams=%d\n", cfg.BufferMB, len(cfg.Cameras))

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastCPU := startCPU
	lastAt := startAt

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	for {
		select {
		case <-done:
			goto finish
		case <-ticker.C:
			now := time.Now()
			cpu := cpuTicks()
			elapsed := now.Sub(lastAt).Seconds()
			if elapsed > 0 {
				pct := (cpu - lastCPU) / elapsed
				var totalBuf int
				for _, r := range rings {
					totalBuf += r.bytes()
				}
				fmt.Printf("[%3.0fs] cpu=%.1f%%  buf_total=%.1fMB\n",
					time.Since(startAt).Seconds(), pct, float64(totalBuf)/(1024*1024))
			}
			lastCPU = cpu
			lastAt = now
		}
	}

finish:
	for s := range results {
		fmt.Printf("%s: main_aus=%d  buf=%.1fMB  motion_frames=%d  sad_triggers=%d\n",
			s.name, s.mainAUs, float64(s.bufBytes)/(1024*1024), s.motionFrames, s.sadTriggers)
	}

	elapsed := time.Since(startAt).Seconds()
	cpuAvg := (cpuTicks() - startCPU) / elapsed
	fmt.Printf("cpu avg: %.1f%%  elapsed: %.0fs\n", cpuAvg, elapsed)
}