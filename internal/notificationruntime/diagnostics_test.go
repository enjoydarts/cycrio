package notificationruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

func TestServicePreservesRepositoryCausePrivately(t *testing.T) {
	original := errors.New("database serialization failed")
	repo := &serviceRepository{err: original}
	_, err := NewService(repo, &serviceSender{}).Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID})
	if !errors.Is(err, original) || !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatalf("cause/public error lost: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", err), original.Error()) {
		t.Fatal("private cause leaked through formatting")
	}
}

func diagnosticChain(err error) string {
	var values []string
	for n := 0; err != nil && n < 16; n++ {
		values = append(values, err.Error())
		err = errors.Unwrap(err)
	}
	return strings.Join(values, "\n")
}

func TestResendRecordsProviderDetailsWithoutDeliveryPayload(t *testing.T) {
	for _, status := range []int{422, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			delivery := notificationDelivery()
			delivery.Candidate.Service = "PRIVATE_MERCHANT_IDENTIFIER"
			calls := 0
			s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(status, `{"name":"validation_error","code":"rate_limit_exceeded","type":"request_error","message":"Rate limit exceeded PRIVATE_MERCHANT verified@example.com synthetic-resend-key","body":"SECRET BODY","headers":{"Authorization":"SECRET HEADER"}}`), nil
			})})
			ctx, scope := diagnostics.WithScope(context.Background())
			result := s.Send(ctx, []byte("synthetic-resend-key"), delivery)
			want := Unknown
			if status == 422 {
				want = Rejected
			}
			if result.State != want || calls != 1 || result.ProviderID != "" {
				t.Fatalf("send outcome/retry changed: %+v %d", result, calls)
			}
			text := diagnosticChain(scope.Error())
			for _, want := range []string{fmt.Sprint(status), "validation_error", "rate_limit_exceeded", "request_error", "Rate limit exceeded"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q: %s", want, text)
				}
			}
			for _, secret := range []string{"PRIVATE_MERCHANT", "verified@example.com", "synthetic-resend-key", "SECRET BODY", "SECRET HEADER"} {
				if strings.Contains(text, secret) {
					t.Errorf("leaked %q: %s", secret, text)
				}
			}
		})
	}
}

func TestResendRecordsTransportCauseWithoutChangingUnknownResult(t *testing.T) {
	s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused synthetic-resend-key verified@example.com")
	})})
	ctx, scope := diagnostics.WithScope(context.Background())
	result := s.Send(ctx, []byte("synthetic-resend-key"), notificationDelivery())
	text := diagnosticChain(scope.Error())
	if result.State != Unknown || !strings.Contains(text, "connection refused") || strings.Contains(text, "synthetic-resend-key") || strings.Contains(text, "verified@example.com") {
		t.Fatalf("transport detail/state unsafe: %+v %s", result, text)
	}
}
