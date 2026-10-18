package testpack

import (
	"testing"
)

func TestFixedInstalledInputContract(t *testing.T) {
	profile, revision, scenario := FixedInstalledInputContract()
	if profile != "corrtest_operational_v1" || revision != "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb" || scenario.InputCount() != 15 || scenario.Budgets != (Budgets{600, 180, 60, 30, 1}) || scenario.Groups != ([4]Group{{"positive", 5, 4, 1}, {"negative", 4, 4, 0}, {"isolated-a", 3, 3, 0}, {"isolated-b", 3, 3, 0}}) {
		t.Fatal("fixed contract changed")
	}
	scenario.Groups[0].Count = 99
	_, _, again := FixedInstalledInputContract()
	if again.Groups[0].Count != 5 {
		t.Fatal("contract aliases mutable data")
	}
	if (&Kit{}).Scenario() != (Scenario{}) {
		t.Fatal("zero kit gained verified scenario")
	}
}
