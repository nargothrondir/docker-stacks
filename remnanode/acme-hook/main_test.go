package main

import (
	"strings"
	"testing"
)

// These tests cover the three guards that each came from an incident, and only
// those. They are not here for coverage: they are here so that deleting a guard
// during a future edit fails CI instead of quietly widening what the hook will
// do with a Cloudflare token.
//
// None of them reaches the network. Every guard runs before the first API call,
// which is itself a property worth keeping.

func TestRecordName(t *testing.T) {
	// Angie strips the leading "*." from a wildcard, so a wildcard certificate
	// for *.example.com arrives as domain=example.com and must still produce the
	// challenge name at the apex.
	if got := recordName("example.com"); got != "_acme-challenge.example.com" {
		t.Fatalf("recordName(example.com) = %q", got)
	}
	if got := recordName("pl-1.example.com"); got != "_acme-challenge.pl-1.example.com" {
		t.Fatalf("recordName(pl-1.example.com) = %q", got)
	}
}

func TestCheckDomain(t *testing.T) {
	zone = "example.com"

	cases := []struct {
		name    string
		domain  string
		refused bool
	}{
		{"the zone itself", "example.com", false},
		{"a node inside the zone", "pl-1.example.com", false},
		{"a deeper name inside the zone", "a.b.example.com", false},

		{"nothing at all", "", true},
		{"an unrelated zone", "evil.net", true},
		// The one that matters: a name that merely STARTS with the zone is not
		// inside it. Without the leading dot in the suffix check, this passes.
		{"the zone as a prefix of another", "example.com.evil.net", true},
		// And the mirror case: a suffix match without the dot boundary.
		{"a longer label ending in the zone", "notexample.com", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDomain(tc.domain)
			if tc.refused && err == nil {
				t.Fatalf("checkDomain(%q) accepted a domain it must refuse", tc.domain)
			}
			if !tc.refused && err != nil {
				t.Fatalf("checkDomain(%q) = %v, want accepted", tc.domain, err)
			}
		})
	}
}

func TestRemoveRefusesEmptyKeyauth(t *testing.T) {
	// An empty value is a caller bug, not an instruction to wipe every record at
	// the name — doing that would break a wildcard challenge running alongside
	// the apex one. The refusal happens before any Cloudflare call, which is why
	// this test needs no network and no credentials.
	err := remove("zone-id", "_acme-challenge.example.com", "")
	if err == nil {
		t.Fatal("remove accepted an empty keyauth: that would delete every record at the name")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("remove failed for the wrong reason: %v", err)
	}
}

func TestDispatchRejectsUnknownOp(t *testing.T) {
	zone = "example.com"
	// A bad domain is rejected before the op is looked at, so use a valid one —
	// and an op that is neither add nor remove must not fall through to either.
	err := dispatch("delete-everything", "pl-1.example.com", "value")
	if err == nil {
		t.Fatal("dispatch accepted an unknown op")
	}
	if !strings.Contains(err.Error(), "unknown op") {
		// Reaching Cloudflare would mean the op switch runs after the network
		// call, which is not the order this service should have.
		t.Fatalf("dispatch failed for the wrong reason: %v", err)
	}
}
