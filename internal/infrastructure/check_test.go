package infrastructure

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckPreservesIDAndPingsDatabase(t *testing.T) {
	const id = "e598ea6b-4ab8-4f29-8311-5aa6aa5c9e01"
	called := false
	result, err := Check(context.Background(), Input{CheckID: id}, func(ctx context.Context) error {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("database ping requires deadline")
		}
		return nil
	})
	if err != nil || !called || result.CheckID != id || result.Status != "ok" {
		t.Fatalf("result = %+v, error = %v, ping = %v", result, err, called)
	}
}

func TestCheckRejectsMissingID(t *testing.T) {
	_, err := Check(context.Background(), Input{}, func(context.Context) error { t.Fatal("unexpected ping"); return nil })
	if err == nil {
		t.Fatal("missing check ID accepted")
	}
}

func TestCheckReturnsGenericDatabaseFailure(t *testing.T) {
	_, err := Check(context.Background(), Input{CheckID: "local-check"}, func(context.Context) error { return errors.New("password=private-secret") })
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestCheckHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, Input{CheckID: "cancelled-check"}, func(ctx context.Context) error { return ctx.Err() })
	if err == nil {
		t.Fatal("cancelled check succeeded")
	}
}
