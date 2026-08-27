package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// runWebServer serves live grid, motion/alerts APIs, media, SSE, optional TLS/ACME.
func runWebServer(ctx context.Context, addr string, workers []*CameraWorker, store *EventStore, logQ chan<- string, tlsDomain, tlsEmail, tlsCert, tlsKey, cfgPath string) {
	if addr == "" {
		return
	}
	byName := make(map[string]*CameraWorker, len(workers))
	for _, w := range workers {
		byName[w.Name()] = w
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte(webIndexHTML(workers)))
	})

	mux.HandleFunc("/snap/", func(rw http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/snap/")
		name = strings.TrimSuffix(name, ".jpg")
		name = strings.TrimSpace(name)
		w := byName[name]
		if w == nil {
			http.NotFound(rw, r)
			return
		}
		jpg, err := w.previewJPEGBytes()
		if err != nil {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.Header().Set("Retry-After", "1")
			rw.WriteHeader(http.StatusServiceUnavailable)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}
		rw.Header().Set("Content-Type", "image/jpeg")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write(jpg)
	})

	mux.HandleFunc("/api/events", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSON(rw, eventPage{Events: []EventRecord{}})
			return
		}
		limit := 100
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				if n > 500 {
					n = 500
				}
				limit = n
			}
		}
		page := store.queryEvents(eventQuery{
			Type:   r.URL.Query().Get("type"),
			Cam:    r.URL.Query().Get("cam"),
			Class:  r.URL.Query().Get("class"),
			Before: parseQueryTime(r.URL.Query().Get("before")),
			Since:  parseQueryTime(r.URL.Query().Get("since")),
			Until:  parseQueryTime(r.URL.Query().Get("until")),
			Limit:  limit,
		})
		writeJSON(rw, page)
	})

	mux.HandleFunc("/api/events/stream", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(rw, "no store", 503)
			return
		}
		flusher, ok := rw.(http.Flusher)
		if !ok {
			http.Error(rw, "no flush", 500)
			return
		}
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		ch := store.Subscribe()
		defer store.Unsubscribe(ch)
		// hello
		fmt.Fprintf(rw, "event: hello\ndata: {}\n\n")
		flusher.Flush()
		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case <-ctx.Done():
				return
			case rec, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(rec)
				fmt.Fprintf(rw, "event: event\ndata: %s\n\n", b)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("/media/", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.NotFound(rw, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/media/")
		abs, err := store.AbsMedia(rel)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		if wq := r.URL.Query().Get("w"); wq != "" {
			n, err := strconv.Atoi(wq)
			if err != nil || n != jpegListThumbW {
				http.Error(rw, "bad w", http.StatusBadRequest)
				return
			}
			thumb, err := ensureJPEGThumb(abs, n)
			if err != nil {
				http.Error(rw, "thumb", http.StatusNotFound)
				return
			}
			rw.Header().Set("Cache-Control", "public, max-age=86400")
			http.ServeFile(rw, r, thumb)
			return
		}
		http.ServeFile(rw, r, abs)
	})

	mux.HandleFunc("/api/settings", func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			out := make([]camMotionSettings, 0, len(workers))
			for _, w := range workers {
				out = append(out, w.MotionSettings())
			}
			writeJSON(rw, map[string]interface{}{"cameras": out})
		case http.MethodPut, http.MethodPost:
			var body struct {
				Cameras []camMotionSettings `json:"cameras"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(rw, err.Error(), 400)
				return
			}
			if err := applyAndSaveSettings(cfgPath, byName, body.Cameras); err != nil {
				http.Error(rw, err.Error(), 400)
				return
			}
			out := make([]camMotionSettings, 0, len(workers))
			for _, w := range workers {
				out = append(out, w.MotionSettings())
			}
			writeJSON(rw, map[string]interface{}{"cameras": out})
		default:
			http.Error(rw, "method", 405)
		}
	})

	mux.HandleFunc("/api/status", func(rw http.ResponseWriter, r *http.Request) {
		type camSt struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		out := make([]camSt, 0, len(workers))
		for _, w := range workers {
			out = append(out, camSt{Name: w.Name(), Status: w.Status()})
		}
		writeJSON(rw, map[string]interface{}{
			"cameras": out,
			"store":   store != nil,
			"secure":  r.TLS != nil || strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1"),
		})
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()

	// Optional Let's Encrypt / cert files
	if tlsDomain != "" && tlsEmail != "" {
		cache := filepath.Join(os.TempDir(), "motioncamotion-certs")
		if store != nil {
			cache = filepath.Join(store.Root(), "certs")
		}
		_ = os.MkdirAll(cache, 0o700)
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(tlsDomain),
			Cache:      autocert.DirCache(cache),
			Email:      tlsEmail,
		}
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate}
		// HTTP-01 on :80 if main is :443-like; also serve challenge
		go func() {
			_ = http.ListenAndServe(":80", m.HTTPHandler(nil))
		}()
		logQ <- fmt.Sprintf("web https://%s/ (acme email=%s)", tlsDomain, tlsEmail)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logQ <- fmt.Sprintf("web server: %v", err)
		}
		return
	}
	if tlsCert != "" && tlsKey != "" {
		logQ <- fmt.Sprintf("web https://0.0.0.0%s/ (cert files)", addr)
		if err := srv.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
			logQ <- fmt.Sprintf("web server: %v", err)
		}
		return
	}

	logQ <- fmt.Sprintf("web http://0.0.0.0%s/ (live + events + SSE /api/events/stream)", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logQ <- fmt.Sprintf("web server: %v", err)
	}
}

func applyAndSaveSettings(path string, byName map[string]*CameraWorker, updates []camMotionSettings) error {
	if path == "" {
		return fmt.Errorf("no config path")
	}
	valid := make([]camMotionSettings, 0, len(updates))
	for _, u := range updates {
		if byName[u.Name] == nil {
			return fmt.Errorf("unknown camera %s", u.Name)
		}
		if u.Threshold <= 0 || u.Threshold > 0.5 {
			return fmt.Errorf("%s: threshold must be >0 and ≤0.5", u.Name)
		}
		if u.MotionMinBlob < 1 || u.MotionMinBlob > 200 {
			return fmt.Errorf("%s: motion_min_blob out of range", u.Name)
		}
		if u.MotionMinCompact < 0 || u.MotionMinCompact > 1 {
			return fmt.Errorf("%s: motion_min_compact out of range", u.Name)
		}
		if u.MotionSustainFrames < 1 || u.MotionSustainFrames > 30 {
			return fmt.Errorf("%s: motion_sustain_frames out of range", u.Name)
		}
		if len(u.MotionMasks) > 8 {
			return fmt.Errorf("%s: too many motion masks", u.Name)
		}
		for i, m := range u.MotionMasks {
			if m.X < 0 || m.Y < 0 || m.W <= 0 || m.H <= 0 || m.X+m.W > 1.02 || m.Y+m.H > 1.02 {
				return fmt.Errorf("%s: motion mask %d out of range", u.Name, i)
			}
		}
		valid = append(valid, u)
	}
	for _, u := range valid {
		byName[u.Name].ApplyMotion(u)
	}
	return patchCameraSettings(path, valid)
}

func writeJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(rw)
	_ = enc.Encode(v)
}

func parseQueryTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func cameraNamesJSON(workers []*CameraWorker) string {
	names := make([]string, 0, len(workers))
	for _, w := range workers {
		names = append(names, w.Name())
	}
	b, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(b)
}
