package fastmail

import (
	"strconv"
	"strings"
)

// validateContentESearch validates the wire representation before the SDK
// normalizes overlapping sets or drops responses with a different correlator.
// Only complete UID result sets can support a negative presence cache.
func validateContentESearch(line []byte, tag string, low, high uint32) error {
	fields := strings.Fields(string(line))
	if len(fields) < 3 {
		return errInvalidFraming
	}
	fields = fields[2:]
	// An ESEARCH response without a correlator is not associated with a
	// particular command (RFC 9051 section 7.3.4), so it cannot establish
	// absence for this request. The installed SDK also requires a correlator
	// to parse a nonempty response; reject instead of silently caching absence.
	if tag == "" || len(fields) < 3 || fields[0] != "(TAG" || fields[1] != strconv.Quote(tag)+")" {
		return errInvalidFraming
	}
	fields = fields[2:]
	// The SDK interprets UID case-sensitively. Require that exact marker to
	// avoid accepting a sequence-number set as a UID result.
	if len(fields) == 0 || fields[0] != "UID" {
		return errInvalidFraming
	}
	fields = fields[1:]
	seenFields := make(map[string]bool, 4)
	uids := make(map[uint32]struct{}, 256)
	values := make(map[string]uint32, 3)
	for len(fields) > 0 {
		if len(fields) < 2 {
			return errInvalidFraming
		}
		name, value := strings.ToUpper(fields[0]), fields[1]
		fields = fields[2:]
		if seenFields[name] {
			return errInvalidFraming
		}
		seenFields[name] = true
		switch name {
		case "ALL":
			for _, part := range strings.Split(value, ",") {
				bounds := strings.Split(part, ":")
				if len(bounds) > 2 {
					return errInvalidFraming
				}
				first, err := strconv.ParseUint(bounds[0], 10, 32)
				if err != nil {
					return errInvalidFraming
				}
				last := first
				if len(bounds) == 2 {
					last, err = strconv.ParseUint(bounds[1], 10, 32)
					if err != nil {
						return errInvalidFraming
					}
				}
				if first > last {
					first, last = last, first
				}
				if first < uint64(low) || last > uint64(high) {
					return errInvalidFraming
				}
				for uid := first; uid <= last; uid++ {
					if _, ok := uids[uint32(uid)]; ok {
						return errInvalidFraming
					}
					uids[uint32(uid)] = struct{}{}
					if len(uids) > 256 {
						return errInvalidFraming
					}
				}
			}
		case "MIN", "MAX", "COUNT":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return errInvalidFraming
			}
			values[name] = uint32(n)
		default:
			return errInvalidFraming
		}
	}
	if count, ok := values["COUNT"]; ok && count != uint32(len(uids)) {
		return errInvalidFraming
	}
	if len(uids) == 0 && (seenFields["MIN"] || seenFields["MAX"]) {
		return errInvalidFraming
	}
	if len(uids) > 0 {
		min, max := high, low
		for uid := range uids {
			if uid < min {
				min = uid
			}
			if uid > max {
				max = uid
			}
		}
		if value, ok := values["MIN"]; ok && value != min {
			return errInvalidFraming
		}
		if value, ok := values["MAX"]; ok && value != max {
			return errInvalidFraming
		}
	}
	return nil
}
