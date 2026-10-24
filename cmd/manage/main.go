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
	case "configure-auth", "migrate", "allow-user", "grant-admin", "revoke-admin", "disable-user":
	default:
		return errors.New("unknown management command")
	}
	flags := flag.NewFlagSet("manage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("bootstrap-file", ".local/bootstrap.json", "public sign-in configuration")
	subject := flags.String("user-id", "", "Clerk user ID")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command options")
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
	store := database.NewStore(pool)
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
