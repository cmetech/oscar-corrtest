package engine

import (
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// Every case reference a built-in campaign runs must pass the runtime's own
// scenario contract; the engine fakes do not validate, so a bad reference
// otherwise fails only live (camp_681887ad6d96c6ff: set B looped ~3.9M times).
func TestBuiltinCaseRefsValidate(t *testing.T) {
	for _, s := range scenario.Builtins() {
		for _, ref := range append(append([]scenario.CaseRef{}, s.Correlation.SetA...), s.Correlation.SetB...) {
			doc, err := caseDoc(ref)
			if err == nil {
				err = corr.Validate(doc)
			}
			if err != nil {
				t.Errorf("%s %s: %v", s.ID, refName(ref), err)
			}
		}
	}
}
