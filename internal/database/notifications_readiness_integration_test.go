//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/notifications"
)

func TestNotificationUnavailableChannelsAllowReduction(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "")
	version := int64(2)
	for _, channel := range []string{"email", "push"} {
		for _, change := range []string{"other_off", "shrink", "in_app", "expand", "new_consent"} {
			t.Run(channel+"/"+change, func(t *testing.T) {
				p := notifications.DefaultPreferences()
				p.Version = version
				version += 2
				p.EmailOptIn, p.PushOptIn = true, true
				p.Kinds[notifications.Renewal] = notifications.Channels{InApp: true, Email: true, Push: true}
				if change == "new_consent" {
					c := p.Kinds[notifications.Renewal]
					if channel == "email" {
						p.EmailOptIn, c.Email = false, false
					} else {
						p.PushOptIn, c.Push = false, false
					}
					p.Kinds[notifications.Renewal] = c
				}
				raw, _ := json.Marshal(p)
				if _, err := r.pool.Exec(ctx, `INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,$2,$3) ON CONFLICT(owner_id) DO UPDATE SET version=EXCLUDED.version,settings=EXCLUDED.settings`, f.owner, p.Version, raw); err != nil {
					t.Fatal(err)
				}
				c := p.Kinds[notifications.Renewal]
				switch change {
				case "other_off":
					if channel == "email" {
						p.PushOptIn, c.Push = false, false
					} else {
						p.EmailOptIn, c.Email = false, false
					}
				case "shrink":
					if channel == "email" {
						c.Email = false
					} else {
						c.Push = false
					}
				case "in_app":
					c.InApp = false
				case "expand":
					extra := p.Kinds[notifications.PendingReview]
					if channel == "email" {
						extra.Email = true
					} else {
						extra.Push = true
					}
					p.Kinds[notifications.PendingReview] = extra
				case "new_consent":
					if channel == "email" {
						p.EmailOptIn = true
					} else {
						p.PushOptIn = true
					}
				}
				p.Kinds[notifications.Renewal] = c
				got, err := r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: p.Version, Confirmed: true})
				if change == "expand" || change == "new_consent" {
					if !errors.Is(err, notifications.ErrNotReady) {
						t.Fatal("拡大を拒否", err)
					}
				} else if err != nil || got.Preferences.EmailOptIn != p.EmailOptIn || got.Preferences.PushOptIn != p.PushOptIn {
					t.Fatal("同意を保った縮小", got, err)
				}
			})
		}
	}
}
