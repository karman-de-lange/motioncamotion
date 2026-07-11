//go:build ignore

package main

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

func main() {
	data, _ := os.ReadFile("cameras.json")
	var cfg struct {
		Cameras []struct {
			URL string `json:"url"`
		} `json:"cameras"`
	}
	json.Unmarshal(data, &cfg)

	var n atomic.Uint64

	u, _ := base.ParseURL(cfg.Cameras[0].URL)
	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host}
	c.Start()
	defer c.Close()

	desc, _, _ := c.Describe(u)
	var forma *format.H265
	medi := desc.FindFormat(&forma)
	c.Setup(desc.BaseURL, medi, 0, 0)
	c.OnPacketRTP(medi, forma, func(_ *rtp.Packet) { n.Add(1) })
	c.Play(nil)

	go func() {
		time.Sleep(2 * time.Minute)
		c.Close()
	}()
	c.Wait()
}