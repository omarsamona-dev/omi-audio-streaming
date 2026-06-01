package main

// Omi Audio Capture Webhook Receiver (CC-PROMPT-101).
//
// Receives the raw PCM16 mono stream that the stock Omi iOS app posts when
// "Realtime audio bytes" is enabled (Settings -> Developer Mode), wraps each
// chunk in a self-contained WAV header, and writes it to a local folder on
// the VPS. Transcription stays in the Omi app; pulling transcripts into
// Moxie Mesh is a separate, later card.
//
// Omi posts:   POST {url}?token={SECRET}&sample_rate=16000&uid={uid}
//              Content-Type: application/octet-stream
//              Body: raw PCM16 mono bytes
//
// Files land at: {AUDIO_DIR}/{uid}/{YYYY-MM-DD}/{unix_ts_ms}.wav  (UTC date)
//
// This file replaces the upstream Google Cloud Storage sink with a local
// folder sink and adds token auth + a uid allow-list. Pure stdlib, no deps.

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const (
	numChannels       = 1     // Mono audio
	bitsPerSample     = 16    // PCM16
	defaultSampleRate = 16000 // Omi CV1 default
	minSampleRate     = 8000
	maxSampleRate     = 192000
)

// uid is used as a path segment, so it must never contain a path separator or
// ".." traversal. Restrict to a conservative slug charset. Omi uids are
// Firebase-style alphanumerics, so this is more than wide enough.
var uidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var (
	captureToken string   // required; set from CAPTURE_TOKEN at startup
	audioDir     string   // AUDIO_DIR, default /data
	allowedUIDs  []string // OMI_ALLOWED_UIDS, comma-separated; empty = allow any
)

// createWAVHeader generates a 44-byte canonical PCM WAV header for dataLength
// bytes of audio at the given sampleRate (mono, 16-bit).
func createWAVHeader(dataLength int, sampleRate int) []byte {
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	header := make([]byte, 44)

	copy(header[0:4], []byte("RIFF"))
	putUint32(header[4:8], uint32(36+dataLength))
	copy(header[8:12], []byte("WAVE"))

	copy(header[12:16], []byte("fmt "))
	putUint32(header[16:20], 16) // fmt chunk size
	putUint16(header[20:22], 1)  // audio format = 1 (PCM)
	putUint16(header[22:24], uint16(numChannels))
	putUint32(header[24:28], uint32(sampleRate))
	putUint32(header[28:32], uint32(byteRate))
	putUint16(header[32:34], uint16(blockAlign))
	putUint16(header[34:36], bitsPerSample)

	copy(header[36:40], []byte("data"))
	putUint32(header[40:44], uint32(dataLength))

	return header
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putUint16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

// tokenOK does a constant-time comparison of the supplied token against the
// configured secret. Length differences are handled by ConstantTimeCompare
// (returns 0), so no early-exit timing leak on length.
func tokenOK(supplied string) bool {
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(captureToken)) == 1
}

// uidAllowed returns true if the allow-list is empty (allow any) or contains uid.
func uidAllowed(uid string) bool {
	if len(allowedUIDs) == 0 {
		return true
	}
	for _, a := range allowedUIDs {
		if subtle.ConstantTimeCompare([]byte(uid), []byte(a)) == 1 {
			return true
		}
	}
	return false
}

func resolveSampleRate(param string) (int, bool) {
	if param == "" {
		return defaultSampleRate, true
	}
	v, err := strconv.Atoi(param)
	if err != nil || v < minSampleRate || v > maxSampleRate {
		return 0, false
	}
	return v, true
}

func handlePostAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()

	// --- AUTH: shared secret in the URL (Omi only lets you set a URL). ---
	// Reject before reading or writing anything. NEVER log the token.
	if !tokenOK(query.Get("token")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		log.Printf("rejected request: bad token (uid=%q)", query.Get("uid"))
		return
	}

	// --- uid validation + allow-list ---
	uid := query.Get("uid")
	if !uidPattern.MatchString(uid) {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		log.Printf("rejected request: invalid uid=%q", uid)
		return
	}
	if !uidAllowed(uid) {
		http.Error(w, "forbidden uid", http.StatusForbidden)
		log.Printf("rejected request: uid not in allow-list (uid=%q)", uid)
		return
	}

	// --- sample_rate (drives the WAV header) ---
	sampleRate, ok := resolveSampleRate(query.Get("sample_rate"))
	if !ok {
		http.Error(w, "invalid sample_rate", http.StatusBadRequest)
		log.Printf("rejected request: invalid sample_rate=%q (uid=%q)", query.Get("sample_rate"), uid)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		// Nothing to persist. Acknowledge so the app doesn't retry-storm.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("empty body, no file written"))
		log.Printf("empty chunk (uid=%s sample_rate=%d)", uid, sampleRate)
		return
	}

	now := time.Now().UTC()
	dateDir := now.Format("2006-01-02")
	dir := filepath.Join(audioDir, uid, dateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "failed to create directory", http.StatusInternalServerError)
		log.Printf("error: mkdir %q failed: %v", dir, err)
		return
	}

	filename := fmt.Sprintf("%d.wav", now.UnixMilli())
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		http.Error(w, "failed to create file", http.StatusInternalServerError)
		log.Printf("error: create %q failed: %v", path, err)
		return
	}

	header := createWAVHeader(len(body), sampleRate)
	if _, err := f.Write(header); err != nil {
		f.Close()
		http.Error(w, "failed to write header", http.StatusInternalServerError)
		log.Printf("error: write header %q failed: %v", path, err)
		return
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		http.Error(w, "failed to write audio", http.StatusInternalServerError)
		log.Printf("error: write audio %q failed: %v", path, err)
		return
	}
	if err := f.Sync(); err != nil {
		log.Printf("warn: fsync %q failed: %v", path, err)
	}
	if err := f.Close(); err != nil {
		http.Error(w, "failed to close file", http.StatusInternalServerError)
		log.Printf("error: close %q failed: %v", path, err)
		return
	}

	log.Printf("wrote uid=%s bytes=%d sample_rate=%d path=%s", uid, len(body), sampleRate, path)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("ok %s", filename)))
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range splitAndTrim(s, ',') {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitAndTrim splits on sep and trims ASCII spaces/tabs from each field.
func splitAndTrim(s string, sep byte) []string {
	fields := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			fields = append(fields, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	fields = append(fields, trimSpace(s[start:]))
	return fields
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func main() {
	captureToken = os.Getenv("CAPTURE_TOKEN")
	if captureToken == "" {
		log.Fatal("CAPTURE_TOKEN is required but not set; refusing to start (fail closed)")
	}

	audioDir = os.Getenv("AUDIO_DIR")
	if audioDir == "" {
		audioDir = "/data"
	}
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		log.Fatalf("cannot create AUDIO_DIR %q: %v", audioDir, err)
	}

	allowedUIDs = splitCSV(os.Getenv("OMI_ALLOWED_UIDS"))
	if len(allowedUIDs) == 0 {
		log.Printf("OMI_ALLOWED_UIDS unset: allowing ANY uid (will log each)")
	} else {
		log.Printf("uid allow-list active (%d entries)", len(allowedUIDs))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/audio", handlePostAudio)
	http.HandleFunc("/healthz", handleHealthz)

	log.Printf("omi-audio receiver starting on :%s (audio_dir=%s)", port, audioDir)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
