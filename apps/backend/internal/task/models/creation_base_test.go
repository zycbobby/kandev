package models

import (
	"strings"
	"testing"
)

func testCreationBase() CreationBase {
	return CreationBase{
		Version: CreationBaseVersion,
		Ref:     "main",
		ForkSHA: strings.Repeat("a", 40),
	}
}

func TestCreationBaseRoundTripsAndPreservesMetadata(t *testing.T) {
	base := testCreationBase()
	metadata := map[string]interface{}{"unrelated": "keep"}
	if err := PutCreationBase(metadata, &base); err != nil {
		t.Fatalf("PutCreationBase: %v", err)
	}

	got, ok, err := LoadCreationBase(metadata)
	if err != nil {
		t.Fatalf("LoadCreationBase: %v", err)
	}
	if !ok || !got.Equal(base) {
		t.Fatalf("loaded creation base = %#v, want %#v", got, base)
	}
	if metadata["unrelated"] != "keep" {
		t.Fatalf("unrelated metadata changed: %#v", metadata)
	}
}

func TestCreationBaseRoundTripsJSONRehydratedMetadata(t *testing.T) {
	metadata := map[string]interface{}{
		CreationBaseMetadataKey: map[string]interface{}{
			"version":  float64(CreationBaseVersion),
			"ref":      "origin/develop",
			"fork_sha": strings.Repeat("b", 40),
		},
	}
	got, ok, err := LoadCreationBase(metadata)
	if err != nil {
		t.Fatalf("LoadCreationBase: %v", err)
	}
	if !ok {
		t.Fatal("LoadCreationBase reported missing key")
	}
	if got.Ref != "origin/develop" || got.ForkSHA != strings.Repeat("b", 40) {
		t.Fatalf("loaded creation base = %#v", got)
	}
}

func TestCreationBaseMissingKeyIsNotAnError(t *testing.T) {
	got, ok, err := LoadCreationBase(map[string]interface{}{"unrelated": "keep"})
	if err != nil {
		t.Fatalf("LoadCreationBase: %v", err)
	}
	if ok || got != (CreationBase{}) {
		t.Fatalf("loaded creation base = %#v, want zero value with ok=false", got)
	}
}

func TestCreationBaseValidationFailsClosed(t *testing.T) {
	base := testCreationBase()
	cases := []struct {
		name   string
		mutate func(*CreationBase)
		want   string
	}{
		{name: "unknown version", mutate: func(v *CreationBase) { v.Version = 99 }, want: "version"},
		{name: "empty ref", mutate: func(v *CreationBase) { v.Ref = "" }, want: "ref"},
		{name: "path traversal ref", mutate: func(v *CreationBase) { v.Ref = "feature/../main" }, want: "ref"},
		{name: "lock suffix ref", mutate: func(v *CreationBase) { v.Ref = "main.lock" }, want: "ref"},
		{name: "symbolic ref", mutate: func(v *CreationBase) { v.Ref = "HEAD" }, want: "ref"},
		{name: "short sha", mutate: func(v *CreationBase) { v.ForkSHA = strings.Repeat("a", 7) }, want: "fork_sha"},
		{name: "non-hex sha", mutate: func(v *CreationBase) { v.ForkSHA = strings.Repeat("z", 40) }, want: "fork_sha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestCreationBaseAcceptsRemoteOnlyRefAndSHA256Fork(t *testing.T) {
	base := testCreationBase()
	base.Ref = "origin/develop"
	base.ForkSHA = strings.Repeat("c", 64)
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate() remote-only ref with SHA-256 fork: %v", err)
	}
}

func TestPutCreationBaseRejectsNilInputs(t *testing.T) {
	if err := PutCreationBase(map[string]interface{}{}, nil); err == nil {
		t.Fatal("PutCreationBase(nil base) should fail")
	}
	base := testCreationBase()
	if err := PutCreationBase(nil, &base); err == nil {
		t.Fatal("PutCreationBase(nil metadata) should fail")
	}
}

func TestRemoveCreationBaseIsIdempotent(t *testing.T) {
	base := testCreationBase()
	metadata := map[string]interface{}{}
	if err := PutCreationBase(metadata, &base); err != nil {
		t.Fatalf("PutCreationBase: %v", err)
	}
	if !RemoveCreationBase(metadata) {
		t.Fatal("first RemoveCreationBase should report removal")
	}
	if RemoveCreationBase(metadata) {
		t.Fatal("second RemoveCreationBase should report nothing left")
	}
	if _, ok, err := LoadCreationBase(metadata); err != nil || ok {
		t.Fatalf("LoadCreationBase after removal = ok %v, err %v", ok, err)
	}
	if RemoveCreationBase(nil) {
		t.Fatal("RemoveCreationBase(nil) should report nothing left")
	}
}
