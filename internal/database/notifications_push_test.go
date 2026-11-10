package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/google/uuid"
)

func TestNotificationDeliveryWithoutKeysDoesNotAccessDatabase(t *testing.T) {
	r := NewNotificationDeliveryRepository(nil, nil, "")
	if _, err := r.DeliverNotification(context.Background(), uuid.NewString(), uuid.NewString(), nil); !errors.Is(err, notificationruntime.ErrUnavailable) {
		t.Fatal(err)
	}
}
