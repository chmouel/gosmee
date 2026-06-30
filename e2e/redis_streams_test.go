//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chmouel/gosmee/gosmee"
	"github.com/redis/go-redis/v9"
)

const (
	contentType          = "application/json"
	redisStreamKeyPrefix = "gosmee:stream:"
	sseEventTimeout      = 10 * time.Second
)

var redisStreamIDPattern = regexp.MustCompile(`^\d+-\d+$`)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type gosmeeProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	logs   *safeBuffer
}

func (p *gosmeeProcess) stop() {
	p.cancel()
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-done
	}
}

type gosmeeServer struct {
	*gosmeeProcess
	url string
}

type serverOptions struct {
	redisStreamMaxLen     int
	replayToken           string
	encryptedChannelsFile string
}

type sseEvent struct {
	ID    string
	Event string
	Data  []byte
}

type sseStream struct {
	cancel context.CancelFunc
	body   io.Closer
	events chan sseEvent
	errs   chan error
}

type gosmeePayload map[string]any

func TestRedisStreamsE2E(t *testing.T) {
	redisURL := testRedisURL(t)
	client := newRedisClient(t, redisURL)
	defer client.Close()

	binary := gosmeeBinary(t)

	t.Run("cross replica webhook delivery and stream resume", func(t *testing.T) {
		channel := uniqueChannel(t, "stream")
		cleanRedisStream(t, client, channel)
		serverA := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})
		serverB := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})

		stream := openSSE(t, serverB.url+"/events/"+channel, "")
		defer stream.close()
		postWebhook(t, serverA.url+"/"+channel, `{"source":"webhook","sequence":1}`, map[string]string{
			"X-GitHub-Delivery": "delivery-one",
			"X-GitHub-Event":    "push",
		})
		first := stream.nextDataEventContaining(t, "delivery-one")
		assertRedisStreamID(t, first.ID)
		assertWebhookPayload(t, first.Data, "delivery-one", "push", `{"source":"webhook","sequence":1}`)
		stream.close()

		postWebhook(t, serverA.url+"/"+channel, `{"source":"webhook","sequence":2}`, map[string]string{
			"X-GitHub-Delivery": "delivery-two",
			"X-GitHub-Event":    "pull_request",
		})

		resumed := openSSE(t, serverB.url+"/events/"+channel, first.ID)
		defer resumed.close()
		second := resumed.nextDataEvent(t)
		if !strings.Contains(string(second.Data), "delivery-two") {
			t.Fatalf("first resumed event was not delivery-two: id=%q data=%s", second.ID, string(second.Data))
		}
		assertRedisStreamID(t, second.ID)
		assertStreamIDAfter(t, second.ID, first.ID)
		assertWebhookPayload(t, second.Data, "delivery-two", "pull_request", `{"source":"webhook","sequence":2}`)
		resumed.close()

		fresh := openSSE(t, serverB.url+"/events/"+channel, "")
		defer fresh.close()
		fresh.expectNoDataEvent(t, 300*time.Millisecond)
		postWebhook(t, serverA.url+"/"+channel, `{"source":"webhook","sequence":3}`, map[string]string{
			"X-GitHub-Delivery": "delivery-three",
			"X-GitHub-Event":    "push",
		})
		third := fresh.nextDataEventContaining(t, "delivery-three")
		assertRedisStreamID(t, third.ID)
		assertStreamIDAfter(t, third.ID, second.ID)
	})

	t.Run("multiple replicas broadcast every stream entry", func(t *testing.T) {
		channel := uniqueChannel(t, "broadcast")
		cleanRedisStream(t, client, channel)
		serverA := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})
		serverB := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})

		streamA := openSSE(t, serverA.url+"/events/"+channel, "")
		defer streamA.close()
		streamB := openSSE(t, serverB.url+"/events/"+channel, "")
		defer streamB.close()

		postWebhook(t, serverA.url+"/"+channel, `{"source":"broadcast"}`, map[string]string{
			"X-GitHub-Delivery": "broadcast-one",
			"X-GitHub-Event":    "push",
		})

		eventA := streamA.nextDataEvent(t)
		eventB := streamB.nextDataEvent(t)
		if eventA.ID != eventB.ID {
			t.Fatalf("broadcast subscribers received different stream IDs: %q and %q", eventA.ID, eventB.ID)
		}
		assertWebhookPayload(t, eventA.Data, "broadcast-one", "push", `{"source":"broadcast"}`)
		assertWebhookPayload(t, eventB.Data, "broadcast-one", "push", `{"source":"broadcast"}`)
	})

	t.Run("protected subscribers receive per-subscriber ciphertext while Redis stores plaintext", func(t *testing.T) {
		channel := uniqueChannel(t, "protected")
		cleanRedisStream(t, client, channel)

		publicKeyA, privateKeyA, err := gosmee.GenerateKeyPair()
		if err != nil {
			t.Fatalf("generating first protected-channel keypair: %v", err)
		}
		publicKeyB, privateKeyB, err := gosmee.GenerateKeyPair()
		if err != nil {
			t.Fatalf("generating second protected-channel keypair: %v", err)
		}
		protectedConfig := map[string]any{
			"channels": map[string]any{
				channel: map[string]any{
					"allowed_public_keys": []string{
						gosmee.EncodePublicKey(publicKeyA),
						gosmee.EncodePublicKey(publicKeyB),
					},
				},
			},
		}
		configData, err := json.Marshal(protectedConfig)
		if err != nil {
			t.Fatalf("encoding protected-channel config: %v", err)
		}
		configPath := filepath.Join(t.TempDir(), "protected-channels.json")
		if err := os.WriteFile(configPath, configData, 0o600); err != nil {
			t.Fatalf("writing protected-channel config: %v", err)
		}

		server := startGosmeeServer(t, binary, redisURL, serverOptions{
			redisStreamMaxLen:     0,
			encryptedChannelsFile: configPath,
		})
		streamURL := server.url + "/events/" + channel
		streamA := openSSE(t, streamURL+"?"+url.Values{"pubkey": {gosmee.EncodePublicKey(publicKeyA)}}.Encode(), "")
		defer streamA.close()
		streamB := openSSE(t, streamURL+"?"+url.Values{"pubkey": {gosmee.EncodePublicKey(publicKeyB)}}.Encode(), "")
		defer streamB.close()

		postWebhook(t, server.url+"/"+channel, `{"source":"protected"}`, map[string]string{
			"X-GitHub-Delivery": "protected-one",
			"X-GitHub-Event":    "push",
		})

		eventA := streamA.nextDataEvent(t)
		eventB := streamB.nextDataEvent(t)
		if eventA.ID != eventB.ID {
			t.Fatalf("protected subscribers received different stream IDs: %q and %q", eventA.ID, eventB.ID)
		}
		if !gosmee.IsEncrypted(eventA.Data) || !gosmee.IsEncrypted(eventB.Data) {
			t.Fatalf("protected subscriber received plaintext: first=%s second=%s", eventA.Data, eventB.Data)
		}
		if bytes.Equal(eventA.Data, eventB.Data) {
			t.Fatal("protected subscribers received identical ciphertext")
		}
		plaintextA, err := gosmee.Decrypt(eventA.Data, privateKeyA)
		if err != nil {
			t.Fatalf("decrypting first subscriber payload: %v", err)
		}
		plaintextB, err := gosmee.Decrypt(eventB.Data, privateKeyB)
		if err != nil {
			t.Fatalf("decrypting second subscriber payload: %v", err)
		}
		if !bytes.Equal(plaintextA, plaintextB) {
			t.Fatalf("protected subscribers decrypted different payloads: %s and %s", plaintextA, plaintextB)
		}
		assertWebhookPayload(t, plaintextA, "protected-one", "push", `{"source":"protected"}`)

		messages, err := client.XRevRangeN(context.Background(), redisStreamKeyPrefix+channel, "+", "-", 1).Result()
		if err != nil {
			t.Fatalf("reading protected Redis stream entry: %v", err)
		}
		if len(messages) != 1 {
			t.Fatalf("expected one protected Redis stream entry, got %d", len(messages))
		}
		storedPayload, ok := messages[0].Values["payload"].(string)
		if !ok {
			t.Fatalf("protected Redis payload has unexpected type %T", messages[0].Values["payload"])
		}
		if gosmee.IsEncrypted([]byte(storedPayload)) {
			t.Fatalf("Redis stored encrypted protected-channel payload: %s", storedPayload)
		}
		if storedPayload != string(plaintextA) {
			t.Fatalf("Redis plaintext differs from subscriber plaintext: %s", storedPayload)
		}
	})

	t.Run("configured max length trims the Redis stream", func(t *testing.T) {
		channel := uniqueChannel(t, "maxlen")
		cleanRedisStream(t, client, channel)
		server := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 10})

		const eventCount = 150
		for i := range eventCount {
			postWebhook(t, server.url+"/"+channel, fmt.Sprintf(`{"sequence":%d}`, i), nil)
		}

		length, err := client.XLen(context.Background(), redisStreamKeyPrefix+channel).Result()
		if err != nil {
			t.Fatalf("reading trimmed Redis stream length: %v", err)
		}
		if length <= 0 || length >= eventCount {
			t.Fatalf("configured max length did not trim Redis stream: got %d entries after %d writes", length, eventCount)
		}
	})

	t.Run("replay endpoint delivers through Redis streams", func(t *testing.T) {
		channel := uniqueChannel(t, "replay")
		cleanRedisStream(t, client, channel)
		const replayToken = "e2e-replay-token"
		serverA := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0, replayToken: replayToken})
		serverB := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0, replayToken: replayToken})

		stream := openSSE(t, serverB.url+"/events/"+channel, "")
		defer stream.close()
		status, body := postJSON(t, serverA.url+"/replay/"+channel, `{"source":"replay"}`, map[string]string{
			"Authorization": "Bearer " + replayToken,
			"X-Replay-ID":   "replay-one",
		})
		if status != http.StatusAccepted {
			t.Fatalf("replay returned %d: %s", status, string(body))
		}
		var replayResp struct {
			Channel  string `json:"channel"`
			Message  string `json:"message"`
			Status   int    `json:"status"`
			StreamID string `json:"stream_id"`
		}
		if err := json.Unmarshal(body, &replayResp); err != nil {
			t.Fatalf("decoding replay response %q: %v", string(body), err)
		}
		if replayResp.Channel != channel || replayResp.Message != "replayed" || replayResp.Status != http.StatusAccepted {
			t.Fatalf("unexpected replay response: %+v", replayResp)
		}
		assertRedisStreamID(t, replayResp.StreamID)

		event := stream.nextDataEventContaining(t, "replay-one")
		assertRedisStreamID(t, event.ID)
		if event.ID != replayResp.StreamID {
			t.Fatalf("SSE event id %q does not match replay stream id %q", event.ID, replayResp.StreamID)
		}
		payload := decodePayload(t, event.Data)
		if payload["authorization"] != nil {
			t.Fatalf("replay payload leaked authorization header: %s", string(event.Data))
		}
		assertBodyB(t, payload, `{"source":"replay"}`)
	})

	t.Run("client resumes from persisted Redis stream checkpoint", func(t *testing.T) {
		channel := uniqueChannel(t, "client")
		cleanRedisStream(t, client, channel)
		server := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})
		target := newTargetServer(t, func(w http.ResponseWriter, r *http.Request, record func([]byte)) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading target body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			record(body)
		})

		stateFile := filepath.Join(t.TempDir(), "resume.state")
		clientProc := startGosmeeClient(t, binary, server.url+"/"+channel, target.URL, stateFile)
		waitForLog(t, clientProc.logs, "Forwarding", 10*time.Second)

		postWebhook(t, server.url+"/"+channel, `{"source":"client","sequence":1}`, map[string]string{
			"X-GitHub-Delivery": "client-one",
			"X-GitHub-Event":    "push",
		})
		target.waitForBody(t, `{"source":"client","sequence":1}`)
		firstID := waitForResumeID(t, stateFile)
		assertRedisStreamID(t, firstID)
		clientProc.stop()

		postWebhook(t, server.url+"/"+channel, `{"source":"client","sequence":2}`, map[string]string{
			"X-GitHub-Delivery": "client-two",
			"X-GitHub-Event":    "push",
		})

		clientProc = startGosmeeClient(t, binary, server.url+"/"+channel, target.URL, stateFile)
		waitForLog(t, clientProc.logs, "Loaded resume checkpoint "+firstID, 10*time.Second)
		target.waitForBody(t, `{"source":"client","sequence":2}`)
		secondID := waitForResumeIDAfter(t, stateFile, firstID)
		assertRedisStreamID(t, secondID)
		assertStreamIDAfter(t, secondID, firstID)
	})

	t.Run("client retries durable events before checkpointing", func(t *testing.T) {
		channel := uniqueChannel(t, "retry")
		cleanRedisStream(t, client, channel)
		server := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})
		var attempts atomic.Int32
		firstAttemptDone := make(chan struct{})
		secondAttemptStarted := make(chan struct{})
		allowSecondAttempt := make(chan struct{}, 1)
		var releaseSecondAttempt sync.Once
		releaseRetry := func() {
			releaseSecondAttempt.Do(func() {
				allowSecondAttempt <- struct{}{}
			})
		}
		t.Cleanup(releaseRetry)
		target := newTargetServer(t, func(w http.ResponseWriter, r *http.Request, record func([]byte)) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading target body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			record(body)
			switch attempts.Add(1) {
			case 1:
				w.WriteHeader(http.StatusInternalServerError)
				close(firstAttemptDone)
				return
			case 2:
				close(secondAttemptStarted)
				<-allowSecondAttempt
			}
			w.WriteHeader(http.StatusOK)
		})

		stateFile := filepath.Join(t.TempDir(), "resume.state")
		clientProc := startGosmeeClient(t, binary, server.url+"/"+channel, target.URL, stateFile)
		waitForLog(t, clientProc.logs, "Forwarding", 10*time.Second)

		postWebhook(t, server.url+"/"+channel, `{"source":"retry"}`, map[string]string{
			"X-GitHub-Delivery": "retry-one",
			"X-GitHub-Event":    "push",
		})
		waitForSignal(t, firstAttemptDone, 10*time.Second, "first failed target attempt")
		waitForSignal(t, secondAttemptStarted, 10*time.Second, "second target attempt")
		if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
			t.Fatalf("resume state exists before the retry succeeds: %v", err)
		}
		releaseRetry()
		if got := attempts.Load(); got < 2 {
			t.Fatalf("expected at least two target attempts, got %d", got)
		}
		assertRedisStreamID(t, waitForResumeID(t, stateFile))
		_ = clientProc
	})

	t.Run("trimmed history emits gap then retained event", func(t *testing.T) {
		channel := uniqueChannel(t, "gap")
		cleanRedisStream(t, client, channel)
		key := redisStreamKeyPrefix + channel
		ctx := context.Background()
		addRedisStreamEntry(t, client, key, "1700000000001-0", `{"old":true}`)
		addRedisStreamEntry(t, client, key, "1700000000002-0", `{"retained":true}`)
		if err := client.Do(ctx, "XTRIM", key, "MINID", "1700000000002-0").Err(); err != nil {
			t.Fatalf("trimming Redis stream: %v", err)
		}

		server := startGosmeeServer(t, binary, redisURL, serverOptions{redisStreamMaxLen: 0})
		stream := openSSE(t, server.url+"/events/"+channel, "1700000000000-0")
		defer stream.close()

		gap := stream.nextEventNamed(t, "gosmee-gap", 10*time.Second)
		if !strings.Contains(string(gap.Data), `"oldest_id":"1700000000002-0"`) {
			t.Fatalf("gap event did not include retained oldest id: %s", string(gap.Data))
		}
		retained := stream.nextDataEventContaining(t, `"retained":true`)
		if retained.ID != "1700000000002-0" {
			t.Fatalf("expected retained event id 1700000000002-0, got %q", retained.ID)
		}
	})
}

func testRedisURL(t *testing.T) string {
	t.Helper()
	if redisURL := os.Getenv("GOSMEE_REDIS_TEST_URL"); redisURL != "" {
		return redisURL
	}
	if redisURL := os.Getenv("GOSMEE_E2E_REDIS_URL"); redisURL != "" {
		return redisURL
	}
	t.Skip("GOSMEE_REDIS_TEST_URL is not set")
	return ""
}

func newRedisClient(t *testing.T, redisURL string) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parsing Redis URL %q: %v", redisURL, err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("connecting to Redis %q: %v", redisURL, err)
	}
	return client
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Dir(filepath.Dir(file))
}

func gosmeeBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("GOSMEE_E2E_BINARY"); binary != "" {
		return binary
	}
	binary := filepath.Join(t.TempDir(), "gosmee")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-mod=vendor", "-o", binary, ".")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building gosmee binary: %v\n%s", err, string(output))
	}
	return binary
}

func startGosmeeServer(t *testing.T, binary, redisURL string, opts serverOptions) *gosmeeServer {
	t.Helper()
	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	args := []string{
		"server",
		"--address", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--public-url", baseURL,
		"--redis-url", redisURL,
		"--redis-stream-maxlen", strconv.Itoa(opts.redisStreamMaxLen),
	}
	if opts.replayToken != "" {
		args = append(args, "--replay-token", opts.replayToken)
	}
	if opts.encryptedChannelsFile != "" {
		args = append(args, "--encrypted-channels-file", opts.encryptedChannelsFile)
	}

	proc := startProcess(t, "gosmee server", binary, args...)
	waitForHTTP(t, baseURL+"/health", proc.logs)
	return &gosmeeServer{
		gosmeeProcess: proc,
		url:           baseURL,
	}
}

func startGosmeeClient(t *testing.T, binary, serverURL, targetURL, stateFile string) *gosmeeProcess {
	t.Helper()
	args := []string{
		"client",
		"--resume-state-file", stateFile,
		"--target-connection-timeout", "1",
		"--nocolor",
		serverURL,
		targetURL,
	}
	return startProcess(t, "gosmee client", binary, args...)
}

func startProcess(t *testing.T, name, binary string, args ...string) *gosmeeProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, args...)
	logs := &safeBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("starting %s: %v", name, err)
	}
	proc := &gosmeeProcess{
		cmd:    cmd,
		cancel: cancel,
		logs:   logs,
	}
	t.Cleanup(proc.stop)
	return proc
}

func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocating port: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("parsing listener address: %v", err)
	}
	parsed, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parsing listener port: %v", err)
	}
	return parsed
}

func waitForHTTP(t *testing.T, url string, logs *safeBuffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx // short readiness poll in test
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %v\nlogs:\n%s", url, lastErr, logs.String())
}

func waitForLog(t *testing.T, logs *safeBuffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log %q\nlogs:\n%s", want, logs.String())
}

func openSSE(t *testing.T, url, lastEventID string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("creating SSE request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("opening SSE stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("opening SSE stream returned %d: %s", resp.StatusCode, string(body))
	}

	stream := &sseStream{
		cancel: cancel,
		body:   resp.Body,
		events: make(chan sseEvent, 16),
		errs:   make(chan error, 1),
	}
	go stream.read(resp.Body)
	return stream
}

func (s *sseStream) read(body io.Reader) {
	defer close(s.events)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var event sseEvent
	var dataLines []string
	dispatch := func() {
		if event.ID == "" && event.Event == "" && len(dataLines) == 0 {
			return
		}
		event.Data = []byte(strings.Join(dataLines, "\n"))
		s.events <- event
		event = sseEvent{}
		dataLines = nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			continue
		default:
			field, value, hasValue := strings.Cut(line, ":")
			if hasValue {
				value = strings.TrimPrefix(value, " ")
			}
			switch field {
			case "id":
				event.ID = value
			case "event":
				event.Event = value
			case "data":
				if hasValue {
					dataLines = append(dataLines, value)
				} else {
					dataLines = append(dataLines, "")
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		s.errs <- err
		return
	}
	s.errs <- nil
}

func (s *sseStream) close() {
	s.cancel()
	_ = s.body.Close()
}

func (s *sseStream) nextDataEvent(t *testing.T) sseEvent {
	t.Helper()
	timer := time.NewTimer(sseEventTimeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-s.events:
			if !ok {
				t.Fatal("SSE stream closed while waiting for next data event")
			}
			if isControlEvent(event) || event.Event != "" || len(event.Data) == 0 {
				continue
			}
			return event
		case err := <-s.errs:
			if err != nil {
				t.Fatalf("SSE stream read failed: %v", err)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for next SSE data event")
		}
	}
}

func (s *sseStream) nextDataEventContaining(t *testing.T, want string) sseEvent {
	t.Helper()
	deadline := time.After(sseEventTimeout)
	var collected []string
	for {
		select {
		case event, ok := <-s.events:
			if !ok {
				t.Fatalf("SSE stream closed while waiting for %q; got %s", want, strings.Join(collected, "\n"))
			}
			if isControlEvent(event) {
				continue
			}
			collected = append(collected, fmt.Sprintf("id=%q event=%q data=%s", event.ID, event.Event, string(event.Data)))
			if event.Event == "" && strings.Contains(string(event.Data), want) {
				return event
			}
		case err := <-s.errs:
			if err != nil {
				t.Fatalf("SSE stream read failed: %v", err)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for SSE payload %q; got %s", want, strings.Join(collected, "\n"))
		}
	}
}

func (s *sseStream) nextEventNamed(t *testing.T, eventName string, timeout time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-s.events:
			if !ok {
				t.Fatalf("SSE stream closed while waiting for event %q", eventName)
			}
			if event.Event == eventName {
				return event
			}
		case err := <-s.errs:
			if err != nil {
				t.Fatalf("SSE stream read failed: %v", err)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for SSE event %q", eventName)
		}
	}
}

func (s *sseStream) expectNoDataEvent(t *testing.T, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-s.events:
			if !ok {
				t.Fatal("SSE stream closed while checking for absent data events")
			}
			if isControlEvent(event) {
				continue
			}
			if event.Event == "" && len(event.Data) > 0 {
				t.Fatalf("unexpected data event before live POST: id=%q data=%s", event.ID, string(event.Data))
			}
		case err := <-s.errs:
			if err != nil {
				t.Fatalf("SSE stream read failed: %v", err)
			}
		case <-timer.C:
			return
		}
	}
}

func isControlEvent(event sseEvent) bool {
	if event.Event == "ready" || event.Event == "ping" {
		return true
	}
	data := string(event.Data)
	return data == "ready" || data == `{"message":"connected"}` || data == `{"message":"ready"}`
}

func postWebhook(t *testing.T, url, body string, headers map[string]string) {
	t.Helper()
	status, respBody := postJSON(t, url, body, headers)
	if status != http.StatusAccepted {
		t.Fatalf("webhook POST returned %d: %s", status, string(respBody))
	}
}

func postJSON(t *testing.T, url, body string, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("creating POST request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading POST response: %v", err)
	}
	return resp.StatusCode, respBody
}

func decodePayload(t *testing.T, data []byte) gosmeePayload {
	t.Helper()
	var payload gosmeePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decoding gosmee payload %s: %v", string(data), err)
	}
	return payload
}

func assertWebhookPayload(t *testing.T, data []byte, delivery, eventType, body string) {
	t.Helper()
	payload := decodePayload(t, data)
	if got := payload["x-github-delivery"]; got != delivery {
		t.Fatalf("x-github-delivery = %v, want %q in %s", got, delivery, string(data))
	}
	if got := payload["x-github-event"]; got != eventType {
		t.Fatalf("x-github-event = %v, want %q in %s", got, eventType, string(data))
	}
	if got := payload["content-type"]; got != contentType {
		t.Fatalf("content-type = %v, want %q in %s", got, contentType, string(data))
	}
	assertBodyB(t, payload, body)
}

func assertBodyB(t *testing.T, payload gosmeePayload, want string) {
	t.Helper()
	bodyB, ok := payload["bodyB"].(string)
	if !ok {
		t.Fatalf("payload missing bodyB string: %#v", payload)
	}
	decoded, err := base64.StdEncoding.DecodeString(bodyB)
	if err != nil {
		t.Fatalf("decoding bodyB: %v", err)
	}
	if string(decoded) != want {
		t.Fatalf("decoded bodyB = %s, want %s", string(decoded), want)
	}
}

func assertRedisStreamID(t *testing.T, id string) {
	t.Helper()
	if !redisStreamIDPattern.MatchString(id) {
		t.Fatalf("invalid Redis stream ID %q", id)
	}
}

func assertStreamIDAfter(t *testing.T, got, previous string) {
	t.Helper()
	gotMS, gotSeq := parseStreamID(t, got)
	prevMS, prevSeq := parseStreamID(t, previous)
	if gotMS < prevMS || gotMS == prevMS && gotSeq <= prevSeq {
		t.Fatalf("stream id %q is not after %q", got, previous)
	}
}

func parseStreamID(t *testing.T, id string) (uint64, uint64) {
	t.Helper()
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		t.Fatalf("invalid stream id %q", id)
	}
	ms, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("invalid stream id %q: %v", id, err)
	}
	seq, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("invalid stream id %q: %v", id, err)
	}
	return ms, seq
}

func uniqueChannel(t *testing.T, name string) string {
	t.Helper()
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("generating random channel: %v", err)
	}
	return "e2e" + name + hex.EncodeToString(raw[:])
}

func cleanRedisStream(t *testing.T, client *redis.Client, channel string) {
	t.Helper()
	key := redisStreamKeyPrefix + channel
	if err := client.Del(context.Background(), key).Err(); err != nil {
		t.Fatalf("cleaning Redis stream for %s: %v", channel, err)
	}
	t.Cleanup(func() {
		if err := client.Del(context.Background(), key).Err(); err != nil {
			t.Errorf("cleaning Redis stream for %s after test: %v", channel, err)
		}
	})
}

func addRedisStreamEntry(t *testing.T, client *redis.Client, key, id, payload string) {
	t.Helper()
	if err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: key,
		ID:     id,
		Values: map[string]any{
			"payload": payload,
		},
	}).Err(); err != nil {
		t.Fatalf("adding Redis stream entry %s: %v", id, err)
	}
}

type targetServer struct {
	*httptest.Server
	bodies chan []byte
}

func newTargetServer(t *testing.T, handler func(http.ResponseWriter, *http.Request, func([]byte))) *targetServer {
	t.Helper()
	target := &targetServer{
		bodies: make(chan []byte, 16),
	}
	target.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, func(body []byte) {
			select {
			case target.bodies <- body:
			default:
			}
		})
	}))
	t.Cleanup(target.Close)
	return target
}

func (s *targetServer) waitForBody(t *testing.T, want string) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case body := <-s.bodies:
		if string(body) != want {
			t.Fatalf("next target body = %s, want %s", string(body), want)
		}
	case <-timer.C:
		t.Fatalf("timed out waiting for target body %s", want)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, timeout time.Duration, description string) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForResumeID(t *testing.T, path string) string {
	t.Helper()
	return waitForResumeIDAfter(t, path, "")
}

func waitForResumeIDAfter(t *testing.T, path, previous string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			id := strings.TrimSpace(string(data))
			if redisStreamIDPattern.MatchString(id) && id != previous {
				return id
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for resume id in %s after %q", path, previous)
	return ""
}
