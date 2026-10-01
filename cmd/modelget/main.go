// modelget — first-run model downloader for Janus.
//
// Downloads a GGUF model from HuggingFace Hub with:
//   - Resumable download (Range header + partial file)
//   - Progress bar with speed and ETA
//   - SHA-256 checksum verification (optional)
//
// Usage:
//
//	modelget -repo meta-llama/Llama-3.2-3B-Instruct -file Llama-3.2-3B-Instruct.Q8_0.gguf -out ./models/
//	modelget -url https://huggingface.co/.../resolve/main/model.gguf -out ./models/
//
// Build:
//
//	go build -o dist/modelget.exe ./cmd/modelget
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var (
		repo     = flag.String("repo", "", "HuggingFace repo, e.g. meta-llama/Llama-3.2-3B-Instruct")
		file     = flag.String("file", "", "Filename inside the repo, e.g. Llama-3.2-3B-Instruct.Q8_0.gguf")
		rawURL   = flag.String("url", "", "Direct download URL (alternative to -repo/-file)")
		outDir   = flag.String("out", "./models", "Output directory")
		expected = flag.String("sha256", "", "Expected SHA-256 hex (optional verification)")
		token    = flag.String("token", "", "HuggingFace API token for gated models (or set HF_TOKEN env)")
	)
	flag.Parse()

	// Resolve the download URL.
	url := strings.TrimSpace(*rawURL)
	if url == "" {
		if *repo == "" || *file == "" {
			log.Fatal("modelget: provide -url OR both -repo and -file")
		}
		url = fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", *repo, *file)
	}

	// Resolve the output file path.
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("modelget: create output dir: %v", err)
	}
	fname := filepath.Base(url)
	if idx := strings.Index(fname, "?"); idx >= 0 {
		fname = fname[:idx]
	}
	outPath := filepath.Join(*outDir, fname)

	// Resolve HF token.
	hfToken := strings.TrimSpace(*token)
	if hfToken == "" {
		hfToken = strings.TrimSpace(os.Getenv("HF_TOKEN"))
	}

	fmt.Printf("modelget: downloading %s\n", url)
	fmt.Printf("modelget: destination  %s\n", outPath)

	if err := download(url, outPath, hfToken); err != nil {
		log.Fatalf("modelget: download failed: %v", err)
	}

	if *expected != "" {
		fmt.Print("modelget: verifying SHA-256… ")
		got, err := sha256File(outPath)
		if err != nil {
			log.Fatalf("\nmodelget: checksum error: %v", err)
		}
		if !strings.EqualFold(got, *expected) {
			os.Remove(outPath)
			log.Fatalf("\nmodelget: checksum MISMATCH\n  expected: %s\n  got:      %s\n  File removed.", *expected, got)
		}
		fmt.Println("OK")
	}

	fmt.Printf("modelget: done → %s\n", outPath)
}

func download(url, outPath, token string) error {
	// Check existing partial file for resume.
	var startByte int64
	if fi, err := os.Stat(outPath); err == nil {
		startByte = fi.Size()
		fmt.Printf("modelget: resuming from byte %d\n", startByte)
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	client := &http.Client{Timeout: 0} // no timeout for large files
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		fmt.Println("modelget: file already complete (server says range not satisfiable)")
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Open file: append if resuming, create if new.
	flag := os.O_CREATE | os.O_WRONLY
	if startByte > 0 && resp.StatusCode == http.StatusPartialContent {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
		startByte = 0
	}
	f, err := os.OpenFile(outPath, flag, 0o644)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer f.Close()

	total := resp.ContentLength
	if total > 0 {
		total += startByte
	}

	// Copy with progress reporting.
	buf := make([]byte, 256*1024) // 256 KiB buffer
	var downloaded int64 = startByte
	start := time.Now()
	lastPrint := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write: %w", werr)
			}
			downloaded += int64(n)
		}
		if time.Since(lastPrint) >= time.Second {
			printProgress(downloaded, total, time.Since(start))
			lastPrint = time.Now()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
	printProgress(downloaded, total, time.Since(start))
	fmt.Println()
	return nil
}

func printProgress(done, total int64, elapsed time.Duration) {
	speed := float64(done) / elapsed.Seconds()
	if total > 0 {
		pct := float64(done) / float64(total) * 100
		remaining := float64(total-done) / speed
		fmt.Printf("\r  %.1f%% — %s / %s — %.1f MB/s — ETA %s   ",
			pct,
			fmtBytes(done), fmtBytes(total),
			speed/1e6,
			fmtDuration(time.Duration(remaining)*time.Second))
	} else {
		fmt.Printf("\r  %s downloaded — %.1f MB/s   ",
			fmtBytes(done), speed/1e6)
	}
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", b/1024)
	}
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
