package notificationruntime

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Requests keep the production URL. Only the test client's TCP dial is routed
// to a loopback TLS server, using that server's trusted certificate.
func loopbackTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.resend.com:443" {
			t.Errorf("unexpected dial target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return transport
}

func TestResendStandardTransportDoesNotReplayLostACK(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if count == 2 {
			// The server consumed the complete delivery, then lost its ACK. A
			// replayable POST on a reused connection is retried by net/http.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("failed to simulate ACK loss")
				return
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"`+providerID+`"}`)
	}))
	defer server.Close()
	transport := loopbackTransport(t, server)
	defer transport.CloseIdleConnections()
	sender := NewResendSender(&http.Client{Transport: transport})
	if result := sender.Send(context.Background(), []byte("synthetic-key"), notificationDelivery()); result.State != Accepted {
		t.Fatal("warmup failed", result)
	}
	second := notificationDelivery()
	second.ID = "44444444-4444-4444-8444-444444444444"
	result := sender.Send(context.Background(), []byte("synthetic-key"), second)
	if result.State != Unknown || requests.Load() != 2 {
		t.Fatalf("lost ACK replayed delivery: state=%s network_requests=%d, want unknown and 2 total requests", result.State, requests.Load())
	}
}

func TestResendStandardTransportUsesHTTP1WithoutMutatingCaller(t *testing.T) {
	var protocol atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol.Store(int32(r.ProtoMajor))
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"id":"`+providerID+`"}`)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := loopbackTransport(t, server)
	transport.ForceAttemptHTTP2 = true
	defer transport.CloseIdleConnections()
	sender := NewResendSender(&http.Client{Transport: transport})
	result := sender.Send(context.Background(), []byte("synthetic-key"), notificationDelivery())
	if result.State != Accepted || protocol.Load() != 1 {
		t.Fatal("notification delivery negotiated retry-capable HTTP/2", result.State, protocol.Load())
	}
	if transport.DisableKeepAlives || !transport.ForceAttemptHTTP2 {
		t.Fatal("constructor mutated caller transport")
	}
}
