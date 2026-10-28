package mailprocessing

import (
	"context"
	"reflect"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestAPISourceStrictDecodeAndFactory(t *testing.T) {
	for _, raw := range []string{`{"Provider":"gmail","RemoteID":"remote-id"}`, `{"provider":"gmail","RemoteID":"remote-id"}`, `{"Provider":"outlook","RemoteID":"remote-id"}`, `{"Provider":"gmail","RemoteID":""}`, `{"Provider":"gmail","RemoteID":"a","RemoteID":"b"}`, `{"Provider":"gmail","RemoteID":"a","extra":1}`, `{"Provider":"gmail","RemoteID":"a"} {}`, `{"UIDValidity":1,"UID":1}`} {
		t.Run(raw, func(t *testing.T) {
			r, keys, lease := pipelineFixture(t)
			r.w.Credential.Provider = "gmail"
			r.w.ReaderFolders = []string{"label-a", "label-b"}
			var err error
			r.w.Envelope, err = keys.Seal([]byte(raw), mailsync.MessageAAD(mailsync.Work{OwnerID: r.w.OwnerID, FolderID: r.w.FolderID, Run: mailsync.Run{AccountID: r.w.Run.AccountID}}, r.w.MessageID))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			s := NewService(r, keys, nil, nil, nil, WithReaderFactory(func(_ context.Context, credential mailaccounts.CredentialVersion) (mailcontent.Reader, error) {
				if credential.Provider != "gmail" {
					t.Fatal("factory scope")
				}
				return textReader(func(_ context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
					calls++
					if source.RemoteID != "remote-id" || source.UID != 0 || source.UIDValidity != 0 || login != "" || len(password) != 0 {
						t.Fatal("remote source")
					}
					if !reflect.DeepEqual(source.AllowedFolders, []string{"label-a", "label-b"}) {
						t.Fatal("reader lost eligible folders", source.AllowedFolders)
					}
					return nil, mailaccounts.AdapterError{Code: "source_missing"}
				}), nil
			}))
			if err = s.Execute(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			want := 0
			if raw == `{"Provider":"gmail","RemoteID":"remote-id"}` {
				want = 1
			}
			if calls != want || r.completion.ErrorCode != "source_missing" {
				t.Fatal(calls, want, r.completion)
			}
		})
	}
}
