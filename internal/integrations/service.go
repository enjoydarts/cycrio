package integrations

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"net/http"
	"time"
)

type Service struct {
	Repository
	secrets Secrets
	sender  Sender
	reader  ProfileReader
}

func NewService(repository Repository, secrets Secrets, sender Sender, readers ...ProfileReader) *Service {
	s := &Service{Repository: repository, secrets: secrets, sender: sender, reader: clerkdelete.NewClient(nil)}
	if len(readers) > 0 && readers[0] != nil {
		s.reader = readers[0]
	}
	return s
}
func (s *Service) SyncProfile(ctx context.Context, actor string, in ProfileSyncInput) (ProfileSyncResult, error) {
	return s.Repository.SyncProfile(ctx, actor, in, s.reader)
}
func (s *Service) Test(ctx context.Context, actor string, in Request) (Attempt, error) {
	return s.Repository.Send(ctx, actor, in, s.sender)
}
func (s *Service) Webhook(ctx context.Context, provider string, body []byte, h http.Header) error {
	if provider != "clerk" && provider != "resend" || len(body) > 256<<10 {
		return ErrInvalid
	}
	id, e := s.WebhookCredential(ctx, provider+"_webhook")
	if e != nil {
		return e
	}
	key, e := s.secrets.Resolve(ctx, id)
	if e != nil {
		return ErrNotReady
	}
	if VerifyWebhook(body, h, key, time.Now()) != nil {
		return ErrInvalid
	}
	if provider == "clerk" {
		return s.ApplyClerk(ctx, id, h.Get("svix-id"), body)
	}
	return s.ApplyResend(ctx, id, h.Get("svix-id"), body)
}
