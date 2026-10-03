package cache

import (
	"context"
	"errors"
)

// Visibility returned by fetch.
type Visibility int

const (
	Public Visibility = iota + 1
	Private
	NoStore
)

// Request is an incoming gateway request.
type Request struct {
	Method  string
	Path    string
	Subject string
	Scopes  []string
	Headers map[string]string
}

// FetchResult is the result of an origin fetch.
type FetchResult struct {
	Status     int
	Visibility Visibility
	TTLMillis  int64
	Vary       []string
	Size       int64
	Body       []byte
}

// FetchFunc fetches one request from the origin.
type FetchFunc func(context.Context, Request) (FetchResult, error)

// Response is what Get returns.
type Response struct {
	Status int
	Body   []byte
}

// Package cache is the auth-aware orchestration layer over key and store.

var (
	errInvalidArgument = errors.New("invalid argument")
	errInvalidTime     = errors.New("invalid time")
	errClockSkew       = errors.New("clock moved backwards")
	errForbidden       = errors.New("forbidden")
	errInvalidResponse = errors.New("invalid fetch response")
)
