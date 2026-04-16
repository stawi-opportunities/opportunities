package connectors

import (
	"encoding/json"
	"strings"
)

// FlexString unmarshals a JSON value that may be a string, an array of strings,
// or null into a single comma-separated string.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = FlexString(strings.Join(arr, ", "))
		return nil
	}
	*f = ""
	return nil
}

func (f FlexString) String() string { return string(f) }
