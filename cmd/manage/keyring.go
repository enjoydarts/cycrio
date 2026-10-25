package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/enjoydarts/cycrio/internal/secretstore"
)

func initializeKeyring(ctx context.Context, path string, hasCredentials func(context.Context) (bool, error), out io.Writer) error {
	if _, err := secretstore.Load(path); err == nil {
		fmt.Fprintln(out, "Encryption keyring ready.")
		return nil
	} else if !errors.Is(err, secretstore.ErrMissing) {
		return errors.New("encryption keyring unavailable")
	}
	exists, err := hasCredentials(ctx)
	if err != nil {
		return errors.New("credential storage check failed")
	}
	if exists {
		return errors.New("encryption keyring missing; restore the existing keyring backup")
	}
	if err := secretstore.Initialize(path); err != nil {
		return errors.New("encryption keyring initialization failed")
	}
	fmt.Fprintln(out, "Encryption keyring ready.")
	return nil
}
