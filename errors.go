package main

import (
	"time"
)

type GenericError struct {
	Description string `json:"description"`
}

type MutedError struct {
	GenericError
	MuteTimeLeft int64 `json:"muteTimeLeft"`
}

func NewMutedError(duration time.Duration) MutedError {
	return MutedError{
		GenericError{"muted"},
		int64(duration / time.Second),
	}
}