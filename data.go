package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// Unpack unpacks an event name, and a byte-slice data from a packed data string.
func Unpack(packed string) (string, []byte, error) {
	// The event name, followed by the data as a string
	parts := strings.SplitN(packed, " ", 2)

	if len(parts) != 2 {
		return "", nil, errors.New("Unable to extract event name from data.")
	}

	return parts[0], []byte(parts[1]), nil
}

// Pack packs an event name and byte data into a string formatted as such: "event_name data_as_string".
func Pack(name string, data []byte) (result []byte) {
	result = append([]byte(name+" "), data...)
}
