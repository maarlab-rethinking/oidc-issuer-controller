/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package verify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameJSON(t *testing.T) {
	if !SameJSON([]byte(`{"a":1,"b":[1,2]}`), []byte("{\n  \"b\": [1, 2],\n  \"a\": 1\n}")) {
		t.Fatal("formatting difference treated as a change")
	}
	if SameJSON([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Fatal("different documents treated as equal")
	}
	if SameJSON([]byte(`<html>`), []byte(`{}`)) {
		t.Fatal("non-JSON treated as equal")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("Cache-Control = %q", r.Header.Get("Cache-Control"))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	f := NewHTTPFetcher()
	body, err := f.Fetch(context.Background(), srv.URL+"/doc")
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("%s %v", body, err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("404 accepted")
	}
}
