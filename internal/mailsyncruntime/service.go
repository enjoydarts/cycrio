package mailsyncruntime

import (
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/fastmail"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(pool *pgxpool.Pool, keys *secretstore.Keyring) *mailsync.Service {
	return mailsync.NewService(database.NewMailSyncRepository(pool), keys, fastmail.NewClient())
}
