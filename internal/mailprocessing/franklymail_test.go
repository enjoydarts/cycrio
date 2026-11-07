package mailprocessing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestFranklyMailProcessingUsesScopedRemoteIdentity(t *testing.T) {
	for _, provider := range []string{"franklymail", "gmail"} {
		r, keys, lease := pipelineFixture(t)
		r.w.Credential.Provider = "franklymail"
		credential, _ := json.Marshal(mailaccounts.ConnectionSecret{Login: []byte("synthetic@example.test"), Password: []byte("synthetic-app-password"), Provider: "franklymail"})
		r.w.Credential.Envelope, _ = keys.Seal(credential, mailaccounts.CredentialAAD(r.w.Credential))
		r.w.ReaderFolders = []string{"jmap-mailbox-a"}
		raw := []byte(fmt.Sprintf(`{"Provider":%q,"RemoteID":"jmap-email-id"}`, provider))
		var err error
		r.w.Envelope, err = keys.Seal(raw, mailsync.MessageAAD(mailsync.Work{OwnerID: r.w.OwnerID, FolderID: r.w.FolderID, Run: mailsync.Run{AccountID: r.w.Run.AccountID}}, r.w.MessageID))
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		s := NewService(r, keys, nil, nil, nil, WithReaderFactory(func(_ context.Context, v mailaccounts.CredentialVersion) (mailcontent.Reader, error) {
			if v.Provider != "franklymail" {
				t.Fatal(v.Provider)
			}
			return textReader(func(_ context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
				calls++
				if source.RemoteID != "jmap-email-id" || source.UID != 0 || source.UIDValidity != 0 || len(source.AllowedFolders) != 1 || login != "synthetic@example.test" || string(password) != "synthetic-app-password" {
					t.Fatal("JMAP source lost its scope")
				}
				return nil, mailaccounts.AdapterError{Code: "source_missing"}
			}), nil
		}))
		if err := s.Execute(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
		want := 0
		if provider == "franklymail" {
			want = 1
		}
		if calls != want || r.completion.ErrorCode != "source_missing" {
			t.Fatal(provider, calls, r.completion)
		}
	}
}
