package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ptypes "github.com/traefik/paerser/types"
	"github.com/traefik/traefik/v3/pkg/config/static"
	tcprouter "github.com/traefik/traefik/v3/pkg/server/router/tcp"
	"github.com/traefik/traefik/v3/pkg/tcp"
	"golang.org/x/net/http2"
)

func TestShutdownHijacked(t *testing.T) {
	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		conn, _, err := rw.(http.Hijacker).Hijack()
		require.NoError(t, err)

		resp := http.Response{StatusCode: http.StatusOK}
		err = resp.Write(conn)
		require.NoError(t, err)
	}))

	testShutdown(t, router)
}

func TestShutdownHTTP(t *testing.T) {
	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		time.Sleep(time.Second)
	}))

	testShutdown(t, router)
}

func TestShutdownTCP(t *testing.T) {
	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	err = router.AddTCPRoute("HostSNI(`*`)", 0, tcp.HandlerFunc(func(conn tcp.WriteCloser) {
		_, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}

		resp := http.Response{StatusCode: http.StatusOK}
		_ = resp.Write(conn)
	}))
	require.NoError(t, err)

	testShutdown(t, router)
}

func testShutdown(t *testing.T, router *tcprouter.Router) {
	t.Helper()

	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()

	epConfig.LifeCycle.RequestAcceptGraceTimeout = 0
	epConfig.LifeCycle.GraceTimeOut = ptypes.Duration(5 * time.Second)
	epConfig.RespondingTimeouts.ReadTimeout = ptypes.Duration(5 * time.Second)
	epConfig.RespondingTimeouts.WriteTimeout = ptypes.Duration(5 * time.Second)

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		// We explicitly use an IPV4 address because on Alpine, with an IPV6 address
		// there seems to be shenanigans related to properly cleaning up file descriptors
		Address:          "127.0.0.1:0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	epAddr := entryPoint.listener.Addr().String()

	request, err := http.NewRequest(http.MethodHead, "http://127.0.0.1:8082", nil)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// We need to do a write on conn before the shutdown to make it "exist".
	// Because the connection indeed exists as far as TCP is concerned,
	// but since we only pass it along to the HTTP server after at least one byte is peeked,
	// the HTTP server (and hence its shutdown) does not know about the connection until that first byte peeked.
	err = request.Write(conn)
	require.NoError(t, err)

	reader := bufio.NewReaderSize(conn, 1)
	// Wait for first byte in response.
	_, err = reader.Peek(1)
	require.NoError(t, err)

	go entryPoint.Shutdown(context.Background())

	// Make sure that new connections are not permitted anymore.
	// Note that this should be true not only after Shutdown has returned,
	// but technically also as early as the Shutdown has closed the listener,
	// i.e. during the shutdown and before the gracetime is over.
	var testOk bool
	for range 10 {
		loopConn, err := net.Dial("tcp", epAddr)
		if err == nil {
			loopConn.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !strings.HasSuffix(err.Error(), "connection refused") && !strings.HasSuffix(err.Error(), "reset by peer") {
			t.Fatalf(`unexpected error: got %v, wanted "connection refused" or "reset by peer"`, err)
		}
		testOk = true
		break
	}
	if !testOk {
		t.Fatal("entry point never closed")
	}

	// And make sure that the connection we had opened before shutting things down is still operational

	resp, err := http.ReadResponse(reader, request)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func startEntrypoint(entryPoint *TCPEntryPoint, router *tcprouter.Router) (net.Conn, error) {
	go entryPoint.Start(context.Background())

	entryPoint.SwitchRouter(router)

	for range 10 {
		conn, err := net.Dial("tcp", entryPoint.listener.Addr().String())
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		return conn, err
	}

	return nil, errors.New("entry point never started")
}

func TestReadTimeoutWithoutFirstByte(t *testing.T) {
	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()
	epConfig.RespondingTimeouts.ReadTimeout = ptypes.Duration(2 * time.Second)

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		Address:          ":0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)

	errChan := make(chan error)

	go func() {
		b := make([]byte, 2048)
		_, err := conn.Read(b)
		errChan <- err
	}()

	select {
	case err := <-errChan:
		require.Equal(t, io.EOF, err)
	case <-time.Tick(5 * time.Second):
		t.Error("Timeout while read")
	}
}

func TestReadTimeoutWithFirstByte(t *testing.T) {
	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()
	epConfig.RespondingTimeouts.ReadTimeout = ptypes.Duration(2 * time.Second)

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		Address:          ":0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)

	_, err = conn.Write([]byte("GET /some HTTP/1.1\r\n"))
	require.NoError(t, err)

	errChan := make(chan error)

	go func() {
		b := make([]byte, 2048)
		_, err := conn.Read(b)
		errChan <- err
	}()

	select {
	case err := <-errChan:
		require.Equal(t, io.EOF, err)
	case <-time.Tick(5 * time.Second):
		t.Error("Timeout while read")
	}
}

func TestKeepAliveMaxRequests(t *testing.T) {
	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()
	epConfig.KeepAliveMaxRequests = 3

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		Address:          ":0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)

	http.DefaultClient.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return conn, nil
		},
	}

	resp, err := http.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.False(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)

	resp, err = http.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.False(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)

	resp, err = http.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.True(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)
}

func TestKeepAliveMaxTime(t *testing.T) {
	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()
	epConfig.KeepAliveMaxTime = ptypes.Duration(time.Millisecond)

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		Address:          ":0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)

	http.DefaultClient.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return conn, nil
		},
	}

	resp, err := http.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.False(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)

	time.Sleep(time.Millisecond)

	resp, err = http.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.True(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)
}

func TestKeepAliveH2c(t *testing.T) {
	epConfig := &static.EntryPointsTransport{}
	epConfig.SetDefaults()
	epConfig.KeepAliveMaxRequests = 1

	entryPoint, err := NewTCPEntryPoint(context.Background(), "", &static.EntryPoint{
		Address:          ":0",
		Transport:        epConfig,
		ForwardedHeaders: &static.ForwardedHeaders{},
		HTTP2:            &static.HTTP2Config{},
	}, nil, nil)
	require.NoError(t, err)

	router, err := tcprouter.NewRouter()
	require.NoError(t, err)

	router.SetHTTPHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))

	conn, err := startEntrypoint(entryPoint, router)
	require.NoError(t, err)

	http2Transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return conn, nil
		},
	}

	client := &http.Client{Transport: http2Transport}

	resp, err := client.Get("http://" + entryPoint.listener.Addr().String())
	require.NoError(t, err)
	require.False(t, resp.Close)
	err = resp.Body.Close()
	require.NoError(t, err)

	_, err = client.Get("http://" + entryPoint.listener.Addr().String())
	require.Error(t, err)
	// Unlike HTTP/1, where we can directly check `resp.Close`, HTTP/2 uses a different
	// mechanism: it sends a GOAWAY frame when the connection is closing.
	// We can only check the error type. The error received should be poll.ErrClosed from
	// the `internal/poll` package, but we cannot directly reference the error type due to
	// package restrictions. Since this error message ("use of closed network connection")
	// is distinct and specific, we rely on its consistency, assuming it is stable and unlikely
	// to change.
	require.Contains(t, err.Error(), "use of closed network connection")
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{path: "/b", expected: "/b"},
		{path: "/b/", expected: "/b/"},
		{path: "/../../b/", expected: "/b/"},
		{path: "/../../b", expected: "/b"},
		{path: "/a/b/..", expected: "/a"},
		{path: "/a/b/../", expected: "/a/"},
		{path: "/a/../../b", expected: "/b"},
		{path: "/..///b///", expected: "/b/"},
		{path: "/a/../b", expected: "/b"},
		{path: "/a/./b", expected: "/a/b"},
		{path: "/a//b", expected: "/a/b"},
		{path: "/a/../../b", expected: "/b"},
		{path: "/a/../c/../b", expected: "/b"},
		{path: "/a/../../../c/../b", expected: "/b"},
		{path: "/a/../c/../../b", expected: "/b"},
		{path: "/a/..//c/.././b", expected: "/b"},
	}

	for _, test := range tests {
		t.Run("Testing case: "+test.path, func(t *testing.T) {
			t.Parallel()

			var callCount int
			clean := sanitizePath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				assert.Equal(t, test.expected, r.URL.Path)
			}))

			request := httptest.NewRequest(http.MethodGet, "http://foo"+test.path, http.NoBody)
			clean.ServeHTTP(httptest.NewRecorder(), request)

			assert.Equal(t, 1, callCount)
		})
	}
}
