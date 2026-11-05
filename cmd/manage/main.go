package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var subjectPattern = regexp.MustCompile(`^user_[A-Za-z0-9_]{1,128}$`)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("management command is required")
	}
	command := args[0]
	switch command {
	case "configure-auth", "migrate", "init-keyring", "allow-user", "grant-admin", "revoke-admin", "disable-user", "recalculate-research-costs", "repair-research-prices", "probe-research":
	default:
		return errors.New("unknown management command")
	}
	flags := flag.NewFlagSet("manage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("bootstrap-file", ".local/bootstrap.json", "public sign-in configuration")
	keyringPath := flags.String("keyring-file", ".local/secrets/keyring.json", "encryption keyring file")
	subject := flags.String("user-id", "", "Clerk user ID")
	apply := flags.Bool("apply", false, "apply research maintenance changes; default is dry run")
	runID := flags.String("run-id", "", "completed manual research run ID")
	probeModels := flags.String("model-ids", "", "1 to 16 exact OpenAI model IDs for one research call")
	probeMaxUSD := flags.String("max-usd", "", "explicit per-call USD reservation, 0.60 to 1.00; session spending must be tracked separately")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command options")
	}
	if *apply && command != "recalculate-research-costs" && command != "repair-research-prices" || *runID != "" && command != "repair-research-prices" || command != "probe-research" && (*probeModels != "" || *probeMaxUSD != "") {
		return errors.New("invalid command options")
	}
	if command == "probe-research" {
		ids, err := researchProbeOptions(*probeModels, *probeMaxUSD)
		if err != nil || *subject != "" {
			return errors.New("invalid research probe options")
		}
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return errors.New("database connection is required")
		}
		return probeResearch(ctx, dsn, ids, out)
	}
	if command == "repair-research-prices" {
		if _, err := uuid.Parse(*runID); err != nil {
			return errors.New("valid research run ID is required")
		}
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return errors.New("database connection is required")
		}
		return repairResearchPrices(ctx, dsn, *runID, *apply, out)
	}
	if command == "recalculate-research-costs" {
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return errors.New("database connection is required")
		}
		return recalculateResearchCosts(ctx, dsn, *apply, out)
	}
	if command == "init-keyring" {
		return initializeKeyring(ctx, *keyringPath, func(ctx context.Context) (bool, error) {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return false, errors.New("database connection is required")
			}
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return false, errors.New("database connection failed")
			}
			defer pool.Close()
			return database.NewCredentialRepository(pool).HasEncryptedCredentials(ctx)
		}, out)
	}
	if command == "configure-auth" {
		scanner := bufio.NewScanner(in)
		fmt.Fprint(out, "Clerk Publishable Key: ")
		if !scanner.Scan() {
			return errors.New("public sign-in configuration is required")
		}
		key := strings.TrimSpace(scanner.Text())
		fmt.Fprint(out, "Frontend origin [http://localhost:3100]: ")
		if !scanner.Scan() {
			return errors.New("frontend origin is required")
		}
		origin := strings.TrimSpace(scanner.Text())
		if origin == "" {
			origin = "http://localhost:3100"
		}
		c := bootstrap.Config{ClerkPublishableKey: key, AllowedOrigins: []string{origin}}
		if err := c.Save(*path); err != nil {
			return err
		}
		fmt.Fprintln(out, "Public sign-in configuration saved. Restart the API to apply it.")
		return nil
	}
	if command != "migrate" && !subjectPattern.MatchString(*subject) {
		return errors.New("valid Clerk user ID is required")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("database connection is required")
	}
	if command == "migrate" {
		if err := database.Migrate(ctx, dsn); err != nil {
			return err
		}
		fmt.Fprintln(out, "Database migrations applied.")
		return nil
	}
	c, err := bootstrap.Load(*path)
	if err != nil {
		return err
	}
	if !c.Configured() {
		return errors.New("configure Clerk sign-in first")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("database connection failed")
	}
	defer pool.Close()
	keys, err := secretstore.Load(*keyringPath)
	if err != nil {
		return err
	}
	store := database.NewStore(pool, keys)
	if command == "grant-admin" || command == "allow-user" {
		if _, err := store.AllowIdentity(ctx, c.Issuer(), *subject, command == "grant-admin"); err != nil {
			return err
		}
	} else {
		p, err := store.FindPrincipal(ctx, c.Issuer(), *subject)
		if err != nil {
			return err
		}
		if command == "revoke-admin" {
			err = store.SetAdmin(ctx, p.ID, false)
		} else {
			err = store.SetUserState(ctx, p.ID, "disabled")
		}
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "User access updated.")
	return nil
}
