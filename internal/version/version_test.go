package version

import (
	"reflect"
	"testing"
)

func TestCurrentReturnsLinkerValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldBuildDate })
	Version, Commit, BuildDate = "v1.2.3", "abc123", "2026-08-19T20:00:00Z"
	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildDate != BuildDate {
		t.Fatalf("Current() = %#v", got)
	}
}

func TestSourceDefaultsAreNonEmpty(t *testing.T) {
	got := Current()
	value := reflect.ValueOf(got)
	typeInfo := value.Type()
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).String() == "" {
			t.Errorf("%s is empty", typeInfo.Field(i).Name)
		}
	}
}
