package server

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	compose_file       = "docker-compose.yml"
	red_container_name = "solana_red"
	red_http_port      = 8891
	blue_http_port     = 8892
	red_ws_port        = 8901
	blue_ws_port       = 8902
	ws_lb_port         = 8888
)

func command(t *testing.T, name string, args ...string) {
	up := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	up.Stdout = &stdout
	up.Stderr = &stderr

	err := up.Run()
	t.Errorf("out:%v", stdout.String())
	t.Errorf("err:%v", stderr.String())
	if err != nil {
		t.Fatalf("Failed to docker-compose up: %v", err)
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		t.Errorf("failed to parse url: %v", err)
	}
	return u
}

func testServerWSURL(t *testing.T, s *httptest.Server) *url.URL {
	u := mustParseURL(t, s.URL)
	return mustParseURL(t, fmt.Sprintf("ws://localhost:%s/", u.Port()))
}

func waitUntilSolanaHealthy(t *testing.T, healthCheckURL *url.URL, checks int, checkTimeout time.Duration) {
	for c := 0; c < checks; c++ {
		res, err := http.Get(healthCheckURL.String())
		if err != nil {
			t.Logf("failed to check health: %v", err)
		} else if res.StatusCode != http.StatusOK {
			t.Logf("check completed but (%s) returned non-OK status returned: %d", healthCheckURL, res.StatusCode)
		} else {
			return
		}
		time.Sleep(checkTimeout)
	}
	t.Errorf("failed to pass health check. check docker compose works outside of test suite")
}

// testWSHandler mocks a Solana server
func testWSHandler(t *testing.T, prefix string, w http.ResponseWriter, r *http.Request) {
	var upgrader = websocket.Upgrader{}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		t.Errorf("upgrade: %v", err)
		return
	}

	// start request goroutine
	var ticker *time.Ticker
	previousCloseHandler := ws.CloseHandler()
	ws.SetCloseHandler(func(code int, text string) error {
		t.Logf("stopping ticker")
		if ticker != nil {
			ticker.Stop()
		}
		return previousCloseHandler(code, text)
	})
	for {
		mt, message, err := ws.ReadMessage()
		if err != nil {
			t.Errorf("test server read: %v", err)
			return
		}
		// t.Logf("%s test server recv: %v %s", prefix, mt, message)
		if ticker == nil && strings.Contains(string(message), "subscribe") {
			// start response goroutine
			go func() {
				ticker = time.NewTicker(400 * time.Millisecond)
				for time := range ticker.C {
					err = ws.WriteMessage(mt, []byte(fmt.Sprintf("%s %v", prefix, time)))
					if err != nil {
						t.Errorf("write: %v", err)
						return
					}
				}
			}()
		}
	}
}

func TestTestWSHandler(t *testing.T) {
	t.Skip("test setup debug")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testWSHandler(t, "", w, r)
	}))
	c, _, err := websocket.DefaultDialer.Dial(testServerWSURL(t, server).String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	c.WriteMessage(websocket.TextMessage, []byte("subscribe"))
	_, msg, err := c.ReadMessage()
	t.Logf("%s %v", msg, err)
}

func TestLocal(t *testing.T) {
	// start red and blue backends
	red_test_server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testWSHandler(t, "red", w, r)
	}))
	blue_test_server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testWSHandler(t, "blue", w, r)
	}))

	// start ws server
	opts := &ServerOpts{
		Main:   testServerWSURL(t, red_test_server),
		Backup: testServerWSURL(t, blue_test_server),
	}
	ws_server := NewServer(opts)
	ws_server_backend := httptest.NewServer(ws_server.Handler())

	// connect to ws server
	c, _, err := websocket.DefaultDialer.Dial(testServerWSURL(t, ws_server_backend).String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	// subscribe to test server and listen for ticks
	c.WriteMessage(websocket.TextMessage, []byte("subscribe"))
	// c.SetReadDeadline(time.Now().Add(time.Second))
	_, res, err := c.ReadMessage()
	if err != nil {
		t.Errorf("test failed to read: %v", err)
	}
	if !strings.Contains(string(res), "red") {
		t.Errorf("expected red response. instead got: %s", res)
	}

	// shutdown red server
	log.Printf("closing red server")
	red_test_server.CloseClientConnections()
	red_test_server.Close()

	// try to read from blue server
	// TODO: queued messages are being returned
	_, res, err = c.ReadMessage()
	if err != nil {
		t.Errorf("test failed to read: %v", err)
	}
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	_, res, _ = c.ReadMessage()
	t.Logf("%s", res)
	// if !strings.Contains(string(res), "blue") {
	// 	t.Errorf("expected blue response. instead got: %s", res)
	// }

	t.Error("failing local test")
}

func TestIntegration(t *testing.T) {
	t.Skip("skipping until local websocket test implemented")

	// start docker container
	command(t, "docker-compose", "--file", compose_file, "up", "--detach",
		"-e", fmt.Sprintf("RED_WS_PORT=%d", red_ws_port),
		"-e", fmt.Sprintf("RED_HTTP_PORT=%d", red_http_port),
		"-e", fmt.Sprintf("BLUE_WS_PORT=%d", blue_ws_port),
		"-e", fmt.Sprintf("BLUE_HTTP_PORT=%d", blue_http_port),
	)
	time.Sleep(20 * time.Second)
	defer command(t, "docker-compose", "--file", compose_file, "down", "--timeout", "1", "--remove-orphans")

	// wait until it is responding
	checks := 10
	check_timeout := time.Second
	waitUntilSolanaHealthy(t, mustParseURL(t, fmt.Sprintf("http://localhost:%d", red_http_port)), checks, check_timeout)
	waitUntilSolanaHealthy(t, mustParseURL(t, fmt.Sprintf("http://localhost:%d", blue_http_port)), checks, check_timeout)

	// start ws server
	// opts := &ServerOpts{
	// 	Main:   mustParseURL(t, fmt.Sprintf("ws://localhost:%d", red_ws_port)),
	// 	Backup: mustParseURL(t, fmt.Sprintf("ws://localhost:%d", blue_ws_port)),
	// }

	// connect to ws server

	// stop Main solana
	// command(t, "docker-compose", "stop", red_container_name, "--timeout", "1")

	// verify data in connection

	// pass/fail
	t.Errorf("manually failing test")
}
