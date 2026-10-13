/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package fakestore is an in-memory store.Store for tests.
package fakestore

import (
	"context"
	"sync"

	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store"
)

// Object is one stored object.
type Object struct {
	Body         []byte
	CacheControl string
}

// Server is a fake bucket shared by every Store it hands out. The zero value is not
// usable; call New.
type Server struct {
	mu        sync.Mutex
	objects   map[string]Object
	accessKey string
	secretKey string
	puts      map[string]int
	failPut   error
}

// New returns a Server accepting the given key pair.
func New(accessKey, secretKey string) *Server {
	return &Server{objects: map[string]Object{}, accessKey: accessKey, secretKey: secretKey, puts: map[string]int{}}
}

// NewStore is a store.NewStoreFunc bound to the server.
func (s *Server) NewStore(cfg store.Config) (store.Store, error) {
	return &fakeStore{s: s, cfg: cfg}, nil
}

// Object returns a stored object.
func (s *Server) Object(key string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	return o, ok
}

// SetObject writes an object directly, as a change made outside the controller would.
func (s *Server) SetObject(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = Object{Body: body}
}

// Puts returns how many times key was written.
func (s *Server) Puts(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts[key]
}

// FailPuts makes every Put return err; nil restores normal behaviour.
func (s *Server) FailPuts(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPut = err
}

type fakeStore struct {
	s   *Server
	cfg store.Config
}

func (f *fakeStore) auth() error {
	if f.cfg.AccessKeyID != f.s.accessKey || f.cfg.SecretAccessKey != f.s.secretKey {
		return &store.CredentialsError{Code: "InvalidAccessKeyId", Message: "unknown key"}
	}
	return nil
}

func (f *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	if err := f.auth(); err != nil {
		return nil, err
	}
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	o, ok := f.s.objects[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return append([]byte(nil), o.Body...), nil
}

func (f *fakeStore) Put(_ context.Context, key string, body []byte, cacheControl string) error {
	if err := f.auth(); err != nil {
		return err
	}
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.failPut != nil {
		return f.s.failPut
	}
	f.s.objects[key] = Object{Body: append([]byte(nil), body...), CacheControl: cacheControl}
	f.s.puts[key]++
	return nil
}
