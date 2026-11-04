package notificationruntime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const leaseID = "55555555-5555-4555-8555-555555555555"

func TestTaskJSONContainsOnlyCanonicalDeliveryAndLeaseIDs(t *testing.T) {
	task := Task{DeliveryID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", LeaseID: leaseID}
	raw, e := json.Marshal(task)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["delivery_id"] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || fields["lease_id"] != leaseID {
		t.Fatal("unsafe task payload", string(raw))
	}
	var decoded Task
	if e = json.Unmarshal(raw, &decoded); e != nil || decoded.DeliveryID != fields["delivery_id"] || decoded.LeaseID != leaseID {
		t.Fatal(decoded, e)
	}
	result, e := json.Marshal(Result{Status: TaskIgnored})
	if e != nil || string(result) != `{"status":"ignored"}` {
		t.Fatal("unexpected worker output", string(result), e)
	}
}

func TestTaskRejectsUnknownDuplicateMissingAndInvalidFields(t *testing.T) {
	valid := `{"delivery_id":"` + deliveryID + `","lease_id":"` + leaseID + `"}`
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"delivery_id":"` + deliveryID + `"}`,
		strings.Replace(valid, `"lease_id"`, `"api_key"`, 1),
		strings.TrimSuffix(valid, "}") + `,"recipient":"verified@example.com"}`,
		strings.TrimSuffix(valid, "}") + `,"delivery_id":"` + deliveryID + `"}`,
		strings.TrimSuffix(valid, "}") + `,"candidate":{}}`,
		strings.Replace(valid, `"`+deliveryID+`"`, `null`, 1),
		strings.Replace(valid, `"`+leaseID+`"`, `false`, 1),
		strings.Replace(valid, deliveryID, "00000000-0000-0000-0000-000000000000", 1),
		strings.Replace(valid, leaseID, "not-a-uuid", 1),
		strings.Replace(valid, deliveryID, "11111111111141118111111111111111", 1),
		valid + ` {}`, strings.Repeat(" ", 513) + valid,
	} {
		target := Task{DeliveryID: deliveryID, LeaseID: leaseID}
		before := target
		e := target.UnmarshalJSON([]byte(raw))
		if !errors.Is(e, ErrInvalidTask) || target != before {
			t.Fatal("unsafe task accepted or partially applied", e, target)
		}
		if strings.Contains(e.Error(), "verified@example.com") {
			t.Fatal("private input leaked in decoder error")
		}
		// encoding/json may reject malformed outer JSON before invoking the
		// custom decoder; that path must also reject the task without mutation.
		if len(raw) <= 512 {
			if e = json.Unmarshal([]byte(raw), &target); e == nil || target != before {
				t.Fatal("outer JSON decoder accepted invalid task")
			}
		}
	}
	for _, task := range []Task{{}, {DeliveryID: deliveryID}, {DeliveryID: deliveryID, LeaseID: "00000000-0000-0000-0000-000000000000"}} {
		if _, e := json.Marshal(task); !errors.Is(e, ErrInvalidTask) {
			t.Fatal("invalid task serialized", e)
		}
	}
}
