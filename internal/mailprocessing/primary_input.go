package mailprocessing

import "github.com/enjoydarts/cycrio/internal/mailcontent"

// LiquidD11StateByteLimit is the limit reported by the hosted d1.1 endpoint's
// invalid_request_error response. Other models and token limits are not inferred
// from it. Inputs below this byte limit can still exceed the token limit.
const LiquidD11StateByteLimit = 250000

// OversizedPrimaryInput identifies confirmed whole inputs that exceed the
// observed primary byte ceiling while retaining the physical message bound.
func OversizedPrimaryInput(w Work, inputBytes int) bool {
	return w.FullEmail && w.Profile.Primary.Provider == "liquid" && w.Profile.Primary.ModelID == "d1.1" && inputBytes > LiquidD11StateByteLimit && inputBytes <= mailcontent.MaxWireBytes
}
