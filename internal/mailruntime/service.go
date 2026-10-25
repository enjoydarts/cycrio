// Package mailruntime wires the owner-scoped mail service to encrypted storage.
package mailruntime

import (
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/fastmail"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Constructing the service performs no IMAP operations. Only an explicit probe
// claimed and run by the Worker may contact Fastmail.
func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *mailaccounts.Service {
	return mailaccounts.NewService(database.NewMailAccountRepository(pool), keyring, fastmail.NewClient())
}
