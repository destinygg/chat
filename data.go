package main

import (
	"encoding/json"
	"errors"
	"strings"
)

func Unpack(data []byte) (string, []byte, error) {
	result := strings.SplitN(string(data), " ", 2)
	if len(result) != 2 {
		return "", nil, errors.New("Unable to extract event name from data.")
	}
	return result[0], []byte(result[1]), nil
}

func Unmarshal(data []byte, structPtr interface{}) error {
	return json.Unmarshal(data, structPtr)
}

func Marshal(structPtr interface{}) ([]byte, error) {
	return json.Marshal(structPtr)
}

func Pack(name string, data []byte) ([]byte, error) {
	result := []byte(name + " ")
	result = append(result, data...)
	return result, nil
}
