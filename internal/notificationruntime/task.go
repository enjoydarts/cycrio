package notificationruntime

import (
	"bytes"
	"encoding/json"
	"io"
)

// Task is the complete Hatchet input. Recipient addresses, credentials and
// notification facts are deliberately resolved by the repository at execution.
type Task struct {
	DeliveryID string `json:"delivery_id"`
	LeaseID    string `json:"lease_id"`
}

func NormalizeTask(task Task) (Task, error) {
	delivery, ok := canonicalID(task.DeliveryID)
	if !ok {
		return Task{}, ErrInvalidTask
	}
	lease, ok := canonicalID(task.LeaseID)
	if !ok {
		return Task{}, ErrInvalidTask
	}
	return Task{DeliveryID: delivery, LeaseID: lease}, nil
}

func (task Task) MarshalJSON() ([]byte, error) {
	normalized, err := NormalizeTask(task)
	if err != nil {
		return nil, err
	}
	type plainTask Task
	return json.Marshal(plainTask(normalized))
}

func (task *Task) UnmarshalJSON(raw []byte) error {
	if task == nil || len(raw) > 512 {
		return ErrInvalidTask
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidTask
	}
	fields := map[string]string{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || key != "delivery_id" && key != "lease_id" {
			return ErrInvalidTask
		}
		if _, exists := fields[key]; exists {
			return ErrInvalidTask
		}
		var value string
		if d.Decode(&value) != nil {
			return ErrInvalidTask
		}
		fields[key] = value
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(fields) != 2 || d.Decode(new(any)) != io.EOF {
		return ErrInvalidTask
	}
	normalized, err := NormalizeTask(Task{DeliveryID: fields["delivery_id"], LeaseID: fields["lease_id"]})
	if err != nil {
		return err
	}
	*task = normalized
	return nil
}
