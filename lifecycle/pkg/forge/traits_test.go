package forge

import (
	"reflect"
	"testing"
)

func TestNormalizeTraits(t *testing.T) {
	got, err := NormalizeTraits([]string{"+ha", "+cgroupv2", "+ha"})
	if err != nil {
		t.Fatalf("NormalizeTraits returned error: %v", err)
	}

	want := []string{"+cgroupv2", "+ha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeTraits = %#v, want %#v", got, want)
	}
}

func TestNormalizeTraitsConflict(t *testing.T) {
	_, err := NormalizeTraits([]string{"+ha", "-ha"})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}
