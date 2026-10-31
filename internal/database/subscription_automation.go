package database

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func automationPreference(ctx context.Context, tx pgx.Tx, owner string) (autoregistration.Preference, error) {
	p := autoregistration.Preference{ApprovalRequired: true}
	err := tx.QueryRow(ctx, `SELECT enabled,version FROM subscription_automation_preferences WHERE owner_id=$1`, owner).Scan(&p.Enabled, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	return p, subscriptionError(err)
}
func (r *SubscriptionRepository) Automation(ctx context.Context, owner string) (autoregistration.Preference, error) {
	tx, err := r.begin(ctx, owner)
	if err != nil {
		return autoregistration.Preference{}, err
	}
	defer tx.Rollback(ctx)
	return automationPreference(ctx, tx, owner)
}
func (r *SubscriptionRepository) SaveAutomation(ctx context.Context, owner string, in autoregistration.PreferenceInput) (autoregistration.Preference, error) {
	if in.ExpectedVersion < 0 {
		return autoregistration.Preference{}, subscriptions.ErrInvalid
	}
	tx, err := r.begin(ctx, owner)
	if err != nil {
		return autoregistration.Preference{}, err
	}
	defer tx.Rollback(ctx)
	p, err := saveAutomationPreference(ctx, tx, owner, in)
	if err != nil {
		return p, err
	}
	return p, subscriptionError(tx.Commit(ctx))
}

// saveAutomationPreference joins an existing owner transaction.
func saveAutomationPreference(ctx context.Context, tx pgx.Tx, owner string, in autoregistration.PreferenceInput) (autoregistration.Preference, error) {
	p, err := automationPreference(ctx, tx, owner)
	if err != nil {
		return p, err
	}
	if p.Version != in.ExpectedVersion {
		return p, subscriptions.ErrConflict
	}
	p.Version++
	p.Enabled = in.Enabled
	_, err = tx.Exec(ctx, `INSERT INTO subscription_automation_preferences(owner_id,version,enabled) VALUES($1,$2,$3) ON CONFLICT(owner_id) DO UPDATE SET version=EXCLUDED.version,enabled=EXCLUDED.enabled`, owner, p.Version, p.Enabled)
	if err != nil {
		return p, subscriptionError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO subscription_automation_preference_audit(id,owner_id,version,enabled) VALUES($1,$2,$3,$4)`, uuid.NewString(), owner, p.Version, p.Enabled)
	if err != nil {
		return p, subscriptionError(err)
	}
	return p, nil
}

// RegisterDue reads only saved results. It has no provider, mailbox or key access.
// Owner locks serialize workers with manual registration and preference changes.
func (r *SubscriptionRepository) RegisterDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, subscriptions.ErrInvalid
	}
	// Extraction now requires explicit owner approval. Keep the worker contract
	// compatible while preserving historical registration data and preferences.
	return 0, nil
}
