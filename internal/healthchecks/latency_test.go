package healthchecks

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestPingWaitsForSlowSuccessButStillBoundsAnOutage(t *testing.T) {
	for _, delay := range []time.Duration{4 * time.Second, 12 * time.Second} {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				select {
				case <-time.After(delay):
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("OK")), Request: r}, nil
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			})})
			err := client.Ping(context.Background(), testURL)
			if delay < 10*time.Second && err != nil {
				t.Fatalf("正常な遅延応答を短すぎる期限で失敗にした: %v", err)
			}
			if delay > 10*time.Second && (err == nil || time.Since(start) != 10*time.Second) {
				t.Fatalf("送信の期限が守られなかった: elapsed=%v err=%v", time.Since(start), err)
			}
		})
	}
}

func TestServiceTickIncludesReadinessAndSlowOutboundSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fixture{enabled: true, claimed: true}
		client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			select {
			case <-time.After(4 * time.Second):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("OK")), Request: r}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		})})
		service := NewService(f, f.Resolve, client, func(context.Context) error { time.Sleep(2 * time.Second); return nil }, func(context.Context) error { return nil })
		if err := service.Tick(context.Background()); err != nil {
			t.Fatalf("実サービス内部の期限が正常な確認・送信を打ち切った: %v", err)
		}
	})
}
