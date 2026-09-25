package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The test binary runs the real server in a child process, with isolated assets.
func TestServerSubprocess(t *testing.T) {
	if os.Getenv("SYMBOL_WEB_TEST_PROCESS") != "1" {
		t.Skip("subprocess helper")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
}

func startTestProcess(t *testing.T, backend string) (string, string, *exec.Cmd, <-chan error) {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"standard.txt", "shadow.txt", "thinkertoy.txt", "templates/index.html", "templates/result.html", "templates/error.html"} {
		data, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestServerSubprocess$", "-test.timeout=40s")
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "SYMBOL_WEB_TEST_PROCESS=1", "ADDR="+addr, "LLM_BASE_URL="+backend, "LLM_MODEL=test-model", "LLM_API_KEY=")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server subprocess failed: %v\n%s", err, output.String())
			}
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("server subprocess did not stop within 3s\n%s", output.String())
		}
	})
	base := "http://" + addr
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(base + "/")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == 200 {
				return base, directory, cmd, done
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subprocess did not become healthy")
	return "", "", nil, nil
}

func checkProcessStatus(t *testing.T, base, path, body string, want int) {
	t.Helper()
	method, contentType := http.MethodGet, ""
	if body != "" {
		method, contentType = http.MethodPost, "application/x-www-form-urlencoded"
	}
	request, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", contentType)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s: status %d want %d, %.100s", path, response.StatusCode, want, data)
	}
}

func TestProcessAssetFailuresRecover(t *testing.T) {
	base, directory, _, _ := startTestProcess(t, "")
	for _, tc := range []struct{ name, path, body string }{
		{"standard.txt", "/symbol-art", "text=Hello&banner=standard"},
		{"templates/index.html", "/", ""},
		{"templates/result.html", "/", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(directory, tc.name)
			original, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			checkProcessStatus(t, base, tc.path, tc.body, 200)
			if err := os.Rename(file, file+".test-backup"); err != nil {
				t.Fatal(err)
			}
			checkProcessStatus(t, base, tc.path, tc.body, 404)
			if err := os.WriteFile(file, []byte("{{if}}"), 0600); err != nil {
				t.Fatal(err)
			}
			checkProcessStatus(t, base, tc.path, tc.body, 500)
			if err := os.WriteFile(file, original, 0600); err != nil {
				t.Fatal(err)
			}
			checkProcessStatus(t, base, tc.path, tc.body, 200)
		})
	}
	if err := os.Rename(filepath.Join(directory, "templates/error.html"), filepath.Join(directory, "templates/error.backup")); err != nil {
		t.Fatal(err)
	}
	checkProcessStatus(t, base, "/missing", "", 404)
	checkProcessStatus(t, base, "/", "", 200)
}

func TestProcessGracefulShutdownFinishesLLM(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{}, 1)
	var once sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		once.Do(func() { close(received) })
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"response":"Hello Team!\nHello World!\nHello Astana!"}`)
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer close(release)
	base, _, cmd, done := startTestProcess(t, backend.URL)
	responses := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Post(base+"/api/suggest", "application/json", strings.NewReader(`{"text":"Hello"}`))
		if err == nil {
			data, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				err = readErr
			} else if response.StatusCode != 200 || !bytes.Contains(data, []byte("Hello Astana!")) {
				err = fmt.Errorf("unexpected LLM reply: %d %s", response.StatusCode, data)
			}
		}
		responses <- err
	}()
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("LLM request did not start")
	}
	checkProcessStatus(t, base, "/", "", 200)
	checkProcessStatus(t, base, "/symbol-art", "text=Hello&banner=standard", 200)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown dropped active request: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	if err := <-responses; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unclean shutdown: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("graceful shutdown did not complete")
	}
}

func TestProcessSlowClientsAndLargeHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("real 5s/15s HTTP deadlines")
	}
	base, _, _, _ := startTestProcess(t, "")
	addr := strings.TrimPrefix(base, "http://")
	t.Run("oversized headers", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Large: %s\r\n\r\n", strings.Repeat("a", 32<<10))
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("status %d want431", response.StatusCode)
		}
	})
	for _, tc := range []struct {
		name, request string
		limit         time.Duration
	}{
		{"slow headers", "GET / HTTP/1.1\r\nHost: localhost\r\nX-Slow:", 5 * time.Second},
		{"slow body", "POST /api/suggest HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{", 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(tc.limit + 3*time.Second))
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			checkProcessStatus(t, base, "/", "", 200)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("server did not disconnect stalled client within deadline")
			}
			if err == nil {
				response.Body.Close()
				if response.StatusCode < 400 {
					t.Fatalf("stalled request succeeded: %d", response.StatusCode)
				}
			}
			elapsed := time.Since(started)
			if elapsed < tc.limit-time.Second {
				t.Fatalf("connection closed too early: %s", elapsed)
			}
			t.Logf("stalled connection released after %s", elapsed.Round(time.Millisecond))
			checkProcessStatus(t, base, "/", "", 200)
		})
	}
	// Disconnect while sending requests; subsequent users must still be served.
	for i := 0; i < 40; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(conn, "POST /api/suggest HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000\r\n\r\n{")
		conn.Close()
	}
	checkProcessStatus(t, base, "/", "", 200)
}
